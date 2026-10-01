package bigip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"myf5-api/pkg/auth"
)

// SCPOptions specifies the SSH connection and authentication configuration for SCP file transfers.
type SCPOptions struct {
	Host       string
	Port       int
	Username   string
	Password   string
	KeyPath    string
	Passphrase string
	Timeout    time.Duration
}

// DefaultSCPTimeout is the default connection timeout for SSH dial operations.
const DefaultSCPTimeout = 30 * time.Second

// CleanSSHHost strips URI schemes, ports, and trailing slashes from a host string.
func CleanSSHHost(rawHost string) (string, int) {
	host := strings.TrimSpace(rawHost)
	port := 22
	if strings.HasPrefix(host, "http://") {
		host = strings.TrimPrefix(host, "http://")
	} else if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
	} else if strings.HasPrefix(host, "ssh://") {
		host = strings.TrimPrefix(host, "ssh://")
	}
	host = strings.TrimRight(host, "/")

	if h, pStr, err := net.SplitHostPort(host); err == nil {
		host = h
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			port = p
		}
	}
	return host, port
}

// BuildSSHClientConfig creates an ssh.ClientConfig resolving keys, agent, and password auth.
func BuildSSHClientConfig(opts SCPOptions) (*ssh.ClientConfig, error) {
	username := opts.Username
	if username == "" {
		username = "admin"
	}

	var authMethods []ssh.AuthMethod

	// 1. Password authentication if provided
	if opts.Password != "" {
		authMethods = append(authMethods, ssh.Password(opts.Password))
		// Also support keyboard-interactive (PAM / TACACS+ / RADIUS)
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = opts.Password
				}
				return answers, nil
			},
		))
	}

	// 2. Explicit private key if provided
	if opts.KeyPath != "" {
		keyBytes, err := os.ReadFile(opts.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key at %s: %w", opts.KeyPath, err)
		}
		var signer ssh.Signer
		if opts.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, []byte(opts.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyBytes)
		}
		// Zero private key file buffer immediately from memory after parsing
		auth.ZeroBytes(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// 3. Fallback to default SSH keys / agent only if no explicit password or key was given
	if opts.Password == "" && opts.KeyPath == "" {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if agentConn, err := net.Dial("unix", sock); err == nil {
				ag := agent.NewClient(agentConn)
				if signers, err := ag.Signers(); err == nil && len(signers) > 0 {
					authMethods = append(authMethods, ssh.PublicKeysCallback(ag.Signers))
				}
			}
		}

		home, err := os.UserHomeDir()
		if err == nil {
			commonKeys := []string{
				filepath.Join(home, ".ssh", "id_ed25519"),
				filepath.Join(home, ".ssh", "id_rsa"),
				filepath.Join(home, ".ssh", "id_ecdsa"),
			}
			var signers []ssh.Signer
			for _, kpath := range commonKeys {
				if keyBytes, err := os.ReadFile(kpath); err == nil {
					signer, parseErr := ssh.ParsePrivateKey(keyBytes)
					// Zero unencrypted private key bytes immediately from memory
					auth.ZeroBytes(keyBytes)
					if parseErr == nil {
						signers = append(signers, signer)
					}
				}
			}
			if len(signers) > 0 {
				authMethods = append(authMethods, ssh.PublicKeys(signers...))
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available (provide password, key, or active SSH agent)")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSCPTimeout
	}

	config := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Intentionally accept appliance host keys in lab and automation environments
		Timeout:         timeout,
	}

	return config, nil
}

// SCPUpload streams a local file to a remote appliance using the standard SCP sink protocol over SSH.
// It reports transfer progress via progressCb.
// destDir defaults to /shared/images for ISO/hotfix files, and /var/tmp for other files.
func SCPUpload(ctx context.Context, localPath string, destDir string, opts SCPOptions, progressCb func(transferred, total int64)) (string, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open local file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to stat local file: %w", err)
	}
	fileSize := stat.Size()
	baseFilename := filepath.Base(localPath)
	filePerm := stat.Mode().Perm()
	if filePerm == 0 || runtime.GOOS == "windows" {
		filePerm = 0644
	}

	// Default destination directories for TMOS / BIG-IQ
	if destDir == "" {
		lowerName := strings.ToLower(baseFilename)
		if strings.HasSuffix(lowerName, ".iso") || strings.HasSuffix(lowerName, ".iso.zip") || strings.Contains(lowerName, "hotfix") {
			destDir = "/shared/images"
		} else {
			destDir = "/var/tmp"
		}
	}
	if err := ValidateRemoteAppliancePath(destDir); err != nil {
		return "", fmt.Errorf("invalid destination directory: %w", err)
	}

	host, port := CleanSSHHost(opts.Host)
	if opts.Port > 0 {
		port = opts.Port
	}
	opts.Host = host
	opts.Port = port

	clientConfig, err := BuildSSHClientConfig(opts)
	if err != nil {
		return "", err
	}

	// Dial with context cancellation support
	var d net.Dialer
	dialTimeout := opts.Timeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultSCPTimeout
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	netConn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("failed connecting to SSH on %s: %w", addr, err)
	}
	defer netConn.Close()

	conn, chans, reqs, err := ssh.NewClientConn(netConn, addr, clientConfig)
	if err != nil {
		return "", fmt.Errorf("SSH handshake failed with %s: %w", addr, err)
	}
	sshClient := ssh.NewClient(conn, chans, reqs)
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	scpCmd := fmt.Sprintf("scp -t %s", destDir)
	if err := session.Start(scpCmd); err != nil {
		return "", fmt.Errorf("failed to start scp sink on remote appliance: %w", err)
	}

	rd := bufio.NewReader(stdout)

	// Step 1: Wait for initial ready acknowledgement from remote scp
	if err := checkAck(rd); err != nil {
		return "", fmt.Errorf("initial scp handshake failed: %w", err)
	}

	// Step 2: Send file header: C<mode> <length> <filename>\n
	header := fmt.Sprintf("C%04o %d %s\n", filePerm, fileSize, baseFilename)
	if _, err := io.WriteString(stdin, header); err != nil {
		return "", fmt.Errorf("failed to send scp header: %w", err)
	}

	// Step 3: Wait for header acknowledgement
	if err := checkAck(rd); err != nil {
		return "", fmt.Errorf("scp header rejected: %w", err)
	}

	// Step 4: Stream file content with progress reporting
	buf := make([]byte, 64*1024)
	var transferred int64 = 0

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		n, rErr := file.Read(buf)
		if n > 0 {
			if _, wErr := stdin.Write(buf[:n]); wErr != nil {
				return "", fmt.Errorf("failed writing to scp session: %w", wErr)
			}
			transferred += int64(n)
			if progressCb != nil {
				progressCb(transferred, fileSize)
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			return "", fmt.Errorf("failed reading local file: %w", rErr)
		}
	}

	// Step 5: Send EOF null byte
	if _, err := stdin.Write([]byte{0}); err != nil {
		return "", fmt.Errorf("failed sending scp EOF byte: %w", err)
	}

	// Step 6: Wait for transfer completion acknowledgement
	if err := checkAck(rd); err != nil {
		return "", fmt.Errorf("scp completion rejected: %w", err)
	}

	_ = stdin.Close()
	if err := session.Wait(); err != nil {
		var exitMissing *ssh.ExitMissingError
		if !errors.As(err, &exitMissing) {
			return "", fmt.Errorf("scp session completed with error: %w", err)
		}
	}

	destPath := path.Join(destDir, baseFilename)
	return destPath, nil
}

// SCPDownload downloads a remote file from a BIG-IP or BIG-IQ appliance to localDest
// using the native Go SCP source protocol (scp -f) over SSH.
// Progress updates are reported via progressCb if non-nil.
func SCPDownload(ctx context.Context, remotePath, localDest string, opts SCPOptions, progressCb func(transferred, total int64)) (string, error) {
	if remotePath == "" {
		return "", fmt.Errorf("remote path cannot be empty")
	}
	if err := ValidateRemoteAppliancePath(remotePath); err != nil {
		return "", fmt.Errorf("invalid remote path: %w", err)
	}
	cleanRemote := path.Clean(remotePath)
	baseFilename := path.Base(cleanRemote)
	if baseFilename == "" || baseFilename == "." || baseFilename == "/" {
		return "", fmt.Errorf("invalid remote file path: %q", remotePath)
	}

	cleanLocal := filepath.Clean(localDest)
	fi, err := os.Stat(cleanLocal)
	if err == nil && fi.IsDir() {
		cleanLocal = filepath.Join(cleanLocal, baseFilename)
	} else if strings.HasSuffix(localDest, string(filepath.Separator)) || strings.HasSuffix(localDest, "/") {
		cleanLocal = filepath.Join(cleanLocal, baseFilename)
	}
	if dir := filepath.Dir(cleanLocal); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return "", fmt.Errorf("failed to create destination directory: %w", err)
		}
	}

	host, port := CleanSSHHost(opts.Host)
	if opts.Port > 0 {
		port = opts.Port
	}
	opts.Host = host
	opts.Port = port

	clientConfig, err := BuildSSHClientConfig(opts)
	if err != nil {
		return "", err
	}

	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))

	var d net.Dialer
	dialTimeout := opts.Timeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultSCPTimeout
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	defer dialCancel()

	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("SSH connection to %s failed: %w", addr, err)
	}
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		return "", fmt.Errorf("SSH handshake failed with %s: %w", addr, err)
	}
	sshClient := ssh.NewClient(sshConn, chans, reqs)
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to open SSH session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("failed to open session stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to open session stdout: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = session.Close()
		_ = sshClient.Close()
	}()

	cmd := fmt.Sprintf("scp -f %s", cleanRemote)
	if err := session.Start(cmd); err != nil {
		return "", fmt.Errorf("failed to start scp on remote appliance: %w", err)
	}

	// Step 1: Send initial null byte to tell server ready to receive
	if _, err := stdin.Write([]byte{0}); err != nil {
		return "", fmt.Errorf("failed to initiate scp handshake: %w", err)
	}

	rd := bufio.NewReader(stdout)

	// Step 2: Read control header line from server
	headerLine, err := rd.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed reading scp header: %w", err)
	}
	headerLine = strings.TrimRight(headerLine, "\r\n")

	// Check if server sent an error (starts with \x01 or \x02)
	if len(headerLine) > 0 && (headerLine[0] == 1 || headerLine[0] == 2) {
		return "", fmt.Errorf("remote scp error: %s", headerLine[1:])
	}

	if !strings.HasPrefix(headerLine, "C") {
		return "", fmt.Errorf("unexpected scp response: %q", headerLine)
	}

	parts := strings.SplitN(headerLine, " ", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("malformed scp file header: %q", headerLine)
	}

	fileSize, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || fileSize < 0 {
		return "", fmt.Errorf("invalid file size in scp header: %q", parts[1])
	}

	// Step 3: Send acknowledgement to begin file transfer
	if _, err := stdin.Write([]byte{0}); err != nil {
		return "", fmt.Errorf("failed sending scp ready ack: %w", err)
	}

	partFile := cleanLocal + ".part"
	outFile, err := os.OpenFile(partFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("failed creating local file %s: %w", partFile, err)
	}

	buf := make([]byte, 64*1024)
	var transferred int64
	var writeErr error

	for transferred < fileSize {
		if ctx.Err() != nil {
			_ = outFile.Close()
			_ = os.Remove(partFile)
			return "", ctx.Err()
		}

		toRead := int64(len(buf))
		if remaining := fileSize - transferred; remaining < toRead {
			toRead = remaining
		}

		n, rErr := rd.Read(buf[:toRead])
		if n > 0 {
			if _, wErr := outFile.Write(buf[:n]); wErr != nil {
				writeErr = wErr
				break
			}
			transferred += int64(n)
			if progressCb != nil {
				progressCb(transferred, fileSize)
			}
		}
		if rErr != nil {
			if rErr == io.EOF && transferred == fileSize {
				break
			}
			writeErr = fmt.Errorf("failed reading scp payload: %w", rErr)
			break
		}
	}

	_ = outFile.Close()
	if writeErr != nil {
		_ = os.Remove(partFile)
		return "", writeErr
	}

	// Step 4: Verify server EOF acknowledgement byte (\x00)
	ackByte, err := rd.ReadByte()
	if err != nil && err != io.EOF {
		_ = os.Remove(partFile)
		return "", fmt.Errorf("failed reading scp completion byte: %w", err)
	}
	if ackByte != 0 && err == nil {
		_ = os.Remove(partFile)
		return "", fmt.Errorf("server sent unexpected scp completion status: %d", ackByte)
	}

	// Step 5: Send final client acknowledgement
	_, _ = stdin.Write([]byte{0})
	_ = stdin.Close()

	if err := session.Wait(); err != nil {
		var exitMissing *ssh.ExitMissingError
		if !errors.As(err, &exitMissing) {
			_ = os.Remove(partFile)
			return "", fmt.Errorf("scp session completed with error: %w", err)
		}
	}

	if err := os.Rename(partFile, cleanLocal); err != nil {
		return "", fmt.Errorf("failed finalizing downloaded file: %w", err)
	}

	return cleanLocal, nil
}

// checkAck reads an acknowledgement byte from the SCP sink stream.
// Byte 0 indicates success. Bytes 1 and 2 indicate warnings or fatal errors with a message string.
func checkAck(r *bufio.Reader) error {
	b, err := r.ReadByte()
	if err != nil {
		return err
	}
	if b == 0 {
		return nil
	}
	message, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("scp error code %d", b)
	}
	return fmt.Errorf("scp error code %d: %s", b, strings.TrimSpace(message))
}
