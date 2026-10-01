package bigip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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
					if signer, err := ssh.ParsePrivateKey(keyBytes); err == nil {
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
	if filePerm == 0 {
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

	destPath := filepath.Join(destDir, baseFilename)
	return destPath, nil
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
