package bigip

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"myf5-cli/pkg/auth"
)

// TransferMethod specifies the network protocol used for file transfers to/from appliances.
type TransferMethod string

const (
	// MethodAuto attempts the primary method and automatically falls back to the alternate method if it fails.
	MethodAuto TransferMethod = "auto"

	// MethodIControl forces the transfer to use the iControl REST API.
	MethodIControl TransferMethod = "icontrol"

	// MethodSCP forces the transfer to use native Go SCP over SSH.
	MethodSCP TransferMethod = "scp"
)

// TransferOptions configures connection, authentication, and destination parameters for appliance file transfers.
type TransferOptions struct {
	TargetType            string         // "BIG-IP" or "BIG-IQ" (default: "BIG-IP")
	Method                TransferMethod // "auto", "icontrol", "scp" (default: "auto")
	Username              string         // Username for appliance authentication
	Password              string         // Password for appliance authentication
	PasswordBytes         []byte         // Mutable byte buffer for password (cleared on Zero)
	Port                  int            // Port override (0 for default: 443 for REST, 22 for SCP)
	IdentityFile          string         // SSH private key file path for public key authentication
	SSHPassphrase         string         // Passphrase for encrypted private key
	SSHPassphraseBytes    []byte         // Mutable byte buffer for SSH passphrase (cleared on Zero)
	KnownHostsPath        string         // Optional OpenSSH known_hosts path for strict SSH host-key verification
	InsecureIgnoreHostKey bool           // Explicitly bypass SSH host-key verification
	DestDir               string         // Remote destination directory for upload (default: "/shared/images")
	ClientCertPath        string         // Path to TLS client certificate file (PEM) for mTLS
	ClientKeyPath         string         // Path to TLS client certificate private key file (PEM)
	Timeout               time.Duration  // Connection timeout
	HTTPClient            *http.Client   // Custom HTTP client (used for in-memory testing or custom transports)
}

// Zero scrubs sensitive password and passphrase bytes and clears string fields from memory.
func (opts *TransferOptions) Zero() {
	if opts == nil {
		return
	}
	if len(opts.PasswordBytes) > 0 {
		auth.ZeroBytes(opts.PasswordBytes)
		opts.PasswordBytes = nil
	}
	if len(opts.SSHPassphraseBytes) > 0 {
		auth.ZeroBytes(opts.SSHPassphraseBytes)
		opts.SSHPassphraseBytes = nil
	}
	opts.Password = ""
	opts.SSHPassphrase = ""
}

// FailoverLogger is a callback invoked when a primary transfer method fails and failover occurs.
type FailoverLogger func(format string, args ...any)

// PushFileWithFailover uploads a local file to a BIG-IP or BIG-IQ appliance.
// In "auto" mode, it attempts the primary method and seamlessly fails over to the alternate method if the first attempt fails.
func PushFileWithFailover(
	ctx context.Context,
	host, localFile string,
	opts TransferOptions,
	logFn FailoverLogger,
	progressCb func(transferred, total int64),
) (string, TransferMethod, error) {
	method := opts.Method
	if method == "" {
		method = MethodAuto
	}

	destDir := opts.DestDir
	if destDir == "" {
		destDir = "/shared/images"
	}

	switch method {
	case MethodIControl:
		destPath, err := pushViaIControl(ctx, host, localFile, destDir, opts, progressCb)
		if err != nil {
			return "", MethodIControl, fmt.Errorf("iControl REST upload failed: %w", err)
		}
		return destPath, MethodIControl, nil

	case MethodSCP:
		destPath, err := pushViaSCP(ctx, host, localFile, destDir, opts, progressCb)
		if err != nil {
			return "", MethodSCP, fmt.Errorf("SCP upload failed: %w", err)
		}
		return destPath, MethodSCP, nil

	case MethodAuto:
		cleanDest := path.Clean(destDir)
		isBigIQ := isBigIQTarget(opts.TargetType)
		hasRestAuth := opts.Password != "" || len(opts.PasswordBytes) > 0 || opts.ClientCertPath != "" || os.Getenv("BIGIP_PASSWORD") != "" || os.Getenv("BIGIQ_PASSWORD") != ""
		// On BIG-IQ, webd blocks /mgmt/tm/util/bash (HTTP 403), so REST can only write to /shared/images and /var/config/rest/downloads.
		bigIQUnsupportedRestDest := isBigIQ && cleanDest != "/shared/images" && cleanDest != "/var/config/rest/downloads"

		// Determine which method to try first based on available credentials and BIG-IQ path capabilities.
		trySCPFirst := opts.IdentityFile != "" || !hasRestAuth || bigIQUnsupportedRestDest

		if trySCPFirst {
			destPath, scpErr := pushViaSCP(ctx, host, localFile, destDir, opts, progressCb)
			if scpErr == nil {
				return destPath, MethodSCP, nil
			}
			if errors.Is(scpErr, context.Canceled) || ctx.Err() != nil {
				return "", MethodSCP, scpErr
			}

			if logFn != nil {
				logFn("SCP upload failed (%v). Automatically falling back to iControl REST...", scpErr)
			}

			destPath, icontrolErr := pushViaIControl(ctx, host, localFile, destDir, opts, progressCb)
			if icontrolErr == nil {
				return destPath, MethodIControl, nil
			}

			return "", MethodAuto, fmt.Errorf("both transfer methods failed:\n  1. SCP error: %v\n  2. iControl REST error: %v", scpErr, icontrolErr)
		}

		// Try iControl REST first
		destPath, icontrolErr := pushViaIControl(ctx, host, localFile, destDir, opts, progressCb)
		if icontrolErr == nil {
			return destPath, MethodIControl, nil
		}
		if errors.Is(icontrolErr, context.Canceled) || ctx.Err() != nil {
			return "", MethodIControl, icontrolErr
		}

		if logFn != nil {
			logFn("iControl REST upload failed (%v). Automatically falling back to SCP over SSH...", icontrolErr)
		}

		destPath, scpErr := pushViaSCP(ctx, host, localFile, destDir, opts, progressCb)
		if scpErr == nil {
			return destPath, MethodSCP, nil
		}

		return "", MethodAuto, fmt.Errorf("both transfer methods failed:\n  1. iControl REST error: %v\n  2. SCP error: %v", icontrolErr, scpErr)

	default:
		return "", method, fmt.Errorf("unsupported transfer method %q (supported: auto, icontrol, scp)", method)
	}
}

// DownloadFileWithFailover downloads a remote file from a BIG-IP or BIG-IQ appliance.
// In "auto" mode, it attempts the primary method and seamlessly fails over to the alternate method if the first attempt fails.
func DownloadFileWithFailover(
	ctx context.Context,
	host, remotePath, localDest string,
	opts TransferOptions,
	logFn FailoverLogger,
	progressCb func(transferred, total int64),
) (string, TransferMethod, error) {
	method := opts.Method
	if method == "" {
		method = MethodAuto
	}

	if err := ValidateApplianceDownloadPath(remotePath); err != nil {
		return "", method, err
	}

	switch method {
	case MethodIControl:
		savedPath, err := downloadViaIControl(ctx, host, remotePath, localDest, opts, progressCb)
		if err != nil {
			return "", MethodIControl, fmt.Errorf("iControl REST download failed: %w", err)
		}
		return savedPath, MethodIControl, nil

	case MethodSCP:
		savedPath, err := downloadViaSCP(ctx, host, remotePath, localDest, opts, progressCb)
		if err != nil {
			return "", MethodSCP, fmt.Errorf("SCP download failed: %w", err)
		}
		return savedPath, MethodSCP, nil

	case MethodAuto:
		// If an explicit SSH identity key (-i) was provided, prefer SCP first.
		// Otherwise, try iControl REST first when REST credentials (password/cert) are available,
		// except for BIG-IQ /var/tmp/qkviews downloads where BIG-IQ lacks qkview-downloads over REST.
		hasRestAuth := opts.Password != "" || len(opts.PasswordBytes) > 0 || opts.ClientCertPath != "" || os.Getenv("BIGIP_PASSWORD") != "" || os.Getenv("BIGIQ_PASSWORD") != ""
		cleanRemote := path.Clean(remotePath)
		bigIQQKViewDownload := isBigIQTarget(opts.TargetType) && (path.Dir(cleanRemote) == "/var/tmp/qkviews" || (!strings.HasPrefix(cleanRemote, "/") && strings.EqualFold(path.Ext(cleanRemote), ".qkview")))
		tryRestFirst := opts.IdentityFile == "" && hasRestAuth && !bigIQQKViewDownload

		if tryRestFirst {
			savedPath, restErr := downloadViaIControl(ctx, host, remotePath, localDest, opts, progressCb)
			if restErr == nil {
				return savedPath, MethodIControl, nil
			}
			if errors.Is(restErr, context.Canceled) || ctx.Err() != nil {
				return "", MethodIControl, restErr
			}

			if logFn != nil {
				logFn("iControl REST download failed (%v). Automatically falling back to SCP over SSH...", restErr)
			}

			savedPath, scpErr := downloadViaSCP(ctx, host, remotePath, localDest, opts, progressCb)
			if scpErr == nil {
				return savedPath, MethodSCP, nil
			}

			return "", MethodAuto, fmt.Errorf("both download methods failed:\n  1. iControl REST error: %v\n  2. SCP error: %v", restErr, scpErr)
		}

		// Try SCP first
		savedPath, scpErr := downloadViaSCP(ctx, host, remotePath, localDest, opts, progressCb)
		if scpErr == nil {
			return savedPath, MethodSCP, nil
		}
		if errors.Is(scpErr, context.Canceled) || ctx.Err() != nil {
			return "", MethodSCP, scpErr
		}

		if logFn != nil {
			logFn("SCP download failed (%v). Automatically falling back to iControl REST...", scpErr)
		}

		savedPath, restErr := downloadViaIControl(ctx, host, remotePath, localDest, opts, progressCb)
		if restErr == nil {
			return savedPath, MethodIControl, nil
		}

		return "", MethodAuto, fmt.Errorf("both download methods failed:\n  1. SCP error: %v\n  2. iControl REST error: %v", scpErr, restErr)

	default:
		return "", method, fmt.Errorf("unsupported transfer method %q (supported: auto, icontrol, scp)", method)
	}
}

func newClientFromOptions(host string, opts TransferOptions, defaultPort int) *Client {
	port := opts.Port
	if port <= 0 {
		port = defaultPort
	}
	user := opts.Username
	if user == "" {
		user = "admin"
	}
	var pwBytes []byte
	if len(opts.PasswordBytes) > 0 {
		pwBytes = make([]byte, len(opts.PasswordBytes))
		copy(pwBytes, opts.PasswordBytes)
	}
	c, err := NewClientWithOptions(ClientOptions{
		Host:           host,
		Port:           port,
		Username:       user,
		Password:       opts.Password,
		PasswordBytes:  pwBytes,
		TargetType:     opts.TargetType,
		SkipTLSVerify:  true,
		ClientCertPath: opts.ClientCertPath,
		ClientKeyPath:  opts.ClientKeyPath,
		Timeout:        opts.Timeout,
		HTTPClient:     opts.HTTPClient,
	})
	if err == nil {
		return c
	}
	c = NewClient(host, port, user, opts.Password)
	c.TargetType = normalizeTargetType(opts.TargetType)
	if opts.HTTPClient != nil {
		c.HTTPClient = opts.HTTPClient
	} else if opts.Timeout > 0 {
		c.HTTPClient.Timeout = opts.Timeout
	}
	return c
}

func pushViaIControl(
	ctx context.Context,
	host, localFile, destDir string,
	opts TransferOptions,
	progressCb func(transferred, total int64),
) (string, error) {
	client := newClientFromOptions(host, opts, 443)
	defer client.Zero()

	cleanDest := path.Clean(destDir)
	if isBigIQTarget(opts.TargetType) {
		if cleanDest == "/var/config/rest/downloads" {
			return client.UploadFile(ctx, localFile, "", progressCb)
		}
		if cleanDest == "/shared/images" {
			uploaded, err := client.UploadSoftwareImage(ctx, localFile, "", progressCb)
			if err == nil {
				return uploaded, nil
			}
			// If a test mock server only implements /mgmt/shared/file-transfer/uploads/, fall back cleanly.
			if !strings.Contains(err.Error(), "HTTP 404") {
				return "", err
			}
			remoteUploaded, upErr := client.UploadFile(ctx, localFile, "", progressCb)
			if upErr != nil {
				return "", err
			}
			promotedPath, copyErr := client.CopyRemoteFile(ctx, remoteUploaded, cleanDest)
			if copyErr != nil {
				return remoteUploaded, fmt.Errorf("uploaded to %s but promotion to %s failed: %w", remoteUploaded, cleanDest, copyErr)
			}
			return promotedPath, nil
		}
		return "", fmt.Errorf("BIG-IQ iControl REST upload only supports --dest /shared/images (via /mgmt/cm/autodeploy/software-image-uploads) or --dest /var/config/rest/downloads (via /mgmt/shared/file-transfer/uploads) because BIG-IQ webd blocks /mgmt/tm/util/bash (got --dest %q); use --method scp for other paths", destDir)
	}

	remoteUploaded, err := client.UploadFile(ctx, localFile, "", progressCb)
	if err != nil {
		return "", err
	}

	if cleanDest != "/var/config/rest/downloads" && cleanDest != "/var/config/rest/downloads/" {
		promotedPath, copyErr := client.CopyRemoteFile(ctx, remoteUploaded, cleanDest)
		if copyErr != nil {
			// If /mgmt/tm/util/bash is disabled (e.g. BIG-IP Appliance Mode) when uploading an ISO to /shared/images,
			// fall back directly to /mgmt/cm/autodeploy/software-image-uploads/<filename>.
			if cleanDest == "/shared/images" && strings.EqualFold(filepath.Ext(localFile), ".iso") {
				if directISO, isoErr := client.UploadSoftwareImage(ctx, localFile, "", progressCb); isoErr == nil {
					return directISO, nil
				}
			}
			return remoteUploaded, fmt.Errorf("uploaded to %s but promotion to %s failed: %w", remoteUploaded, cleanDest, copyErr)
		}
		return promotedPath, nil
	}

	return remoteUploaded, nil
}

func newSCPOptions(host string, opts TransferOptions) SCPOptions {
	var pwBytes []byte
	if len(opts.PasswordBytes) > 0 {
		pwBytes = make([]byte, len(opts.PasswordBytes))
		copy(pwBytes, opts.PasswordBytes)
	}
	var passBytes []byte
	if len(opts.SSHPassphraseBytes) > 0 {
		passBytes = make([]byte, len(opts.SSHPassphraseBytes))
		copy(passBytes, opts.SSHPassphraseBytes)
	}
	return SCPOptions{
		Host:                  host,
		Port:                  opts.Port,
		Username:              opts.Username,
		Password:              opts.Password,
		PasswordBytes:         pwBytes,
		KeyPath:               opts.IdentityFile,
		Passphrase:            opts.SSHPassphrase,
		PassphraseBytes:       passBytes,
		KnownHostsPath:        opts.KnownHostsPath,
		InsecureIgnoreHostKey: opts.InsecureIgnoreHostKey,
		Timeout:               opts.Timeout,
	}
}

func pushViaSCP(
	ctx context.Context,
	host, localFile, destDir string,
	opts TransferOptions,
	progressCb func(transferred, total int64),
) (string, error) {
	scpOpts := newSCPOptions(host, opts)
	defer scpOpts.Zero()
	return SCPUpload(ctx, localFile, destDir, scpOpts, progressCb)
}

func downloadViaIControl(
	ctx context.Context,
	host, remotePath, localDest string,
	opts TransferOptions,
	progressCb func(transferred, total int64),
) (string, error) {
	client := newClientFromOptions(host, opts, 443)
	defer client.Zero()

	cleanLocal := filepath.Clean(localDest)
	base := path.Base(path.Clean(remotePath))
	fi, err := os.Stat(cleanLocal)
	if err == nil && fi.IsDir() {
		cleanLocal = filepath.Join(cleanLocal, base)
	} else if strings.HasSuffix(localDest, string(filepath.Separator)) || strings.HasSuffix(localDest, "/") {
		cleanLocal = filepath.Join(cleanLocal, base)
	}

	err = client.DownloadFile(ctx, remotePath, cleanLocal, progressCb)
	if err != nil {
		return "", err
	}
	return cleanLocal, nil
}

func downloadViaSCP(
	ctx context.Context,
	host, remotePath, localDest string,
	opts TransferOptions,
	progressCb func(transferred, total int64),
) (string, error) {
	scpOpts := newSCPOptions(host, opts)
	defer scpOpts.Zero()
	return SCPDownload(ctx, remotePath, localDest, scpOpts, progressCb)
}

// GenerateQKViewWithFailover generates a diagnostic QKView via iControl REST or SSH.
// In "auto" mode, it chooses based on available credentials and auto-fails over if one fails.
func GenerateQKViewWithFailover(
	ctx context.Context,
	host string,
	requestedFilename string,
	complete bool,
	opts TransferOptions,
	logFn FailoverLogger,
	statusCb func(status string),
) (string, TransferMethod, error) {
	method := opts.Method
	if method == "" {
		method = MethodAuto
	}

	switch method {
	case MethodIControl:
		client := newClientFromOptions(host, opts, 443)
		defer client.Zero()
		filename, err := client.GenerateQKViewWithStatus(ctx, requestedFilename, complete, statusCb)
		if err != nil {
			return "", MethodIControl, fmt.Errorf("iControl REST QKView generation failed: %w", err)
		}
		return filename, MethodIControl, nil

	case MethodSCP:
		scpOpts := newSCPOptions(host, opts)
		defer scpOpts.Zero()
		filename, err := GenerateQKViewSSH(ctx, scpOpts, requestedFilename, complete, statusCb)
		if err != nil {
			return "", MethodSCP, fmt.Errorf("SSH QKView generation failed: %w", err)
		}
		return filename, MethodSCP, nil

	case MethodAuto:
		// BIG-IQ does not expose /mgmt/cm/autodeploy/qkview or /mgmt/tm/util/qkview and its webd
		// blocks /mgmt/tm/util/bash (HTTP 403), so prefer SSH first when TargetType is BIG-IQ.
		hasRestAuth := opts.Password != "" || len(opts.PasswordBytes) > 0 || opts.ClientCertPath != "" || os.Getenv("BIGIP_PASSWORD") != "" || os.Getenv("BIGIQ_PASSWORD") != ""
		tryRestFirst := opts.IdentityFile == "" && hasRestAuth && !isBigIQTarget(opts.TargetType)

		if tryRestFirst {
			client := newClientFromOptions(host, opts, 443)
			filename, restErr := client.GenerateQKViewWithStatus(ctx, requestedFilename, complete, statusCb)
			client.Zero()
			if restErr == nil {
				return filename, MethodIControl, nil
			}
			if errors.Is(restErr, context.Canceled) || ctx.Err() != nil {
				return "", MethodIControl, restErr
			}

			if logFn != nil {
				logFn("iControl REST QKView generation failed (%v). Automatically falling back to SSH...", restErr)
			}

			scpOpts := newSCPOptions(host, opts)
			defer scpOpts.Zero()
			filename, sshErr := GenerateQKViewSSH(ctx, scpOpts, requestedFilename, complete, statusCb)
			if sshErr == nil {
				return filename, MethodSCP, nil
			}

			return "", MethodAuto, fmt.Errorf("both QKView generation methods failed:\n  1. iControl REST error: %v\n  2. SSH error: %v", restErr, sshErr)
		}

		// Try SSH first
		scpOpts := newSCPOptions(host, opts)
		filename, sshErr := GenerateQKViewSSH(ctx, scpOpts, requestedFilename, complete, statusCb)
		scpOpts.Zero()
		if sshErr == nil {
			return filename, MethodSCP, nil
		}
		if errors.Is(sshErr, context.Canceled) || ctx.Err() != nil {
			return "", MethodSCP, sshErr
		}

		if logFn != nil {
			logFn("SSH QKView generation failed (%v). Automatically falling back to iControl REST...", sshErr)
		}

		client := newClientFromOptions(host, opts, 443)
		defer client.Zero()
		filename, restErr := client.GenerateQKViewWithStatus(ctx, requestedFilename, complete, statusCb)
		if restErr == nil {
			return filename, MethodIControl, nil
		}

		return "", MethodAuto, fmt.Errorf("both QKView generation methods failed:\n  1. SSH error: %v\n  2. iControl REST error: %v", sshErr, restErr)

	default:
		return "", method, fmt.Errorf("unsupported transfer method %q", method)
	}
}

// DeleteQKViewWithFailover deletes a generated QKView archive on the appliance via REST or SSH.
func DeleteQKViewWithFailover(ctx context.Context, host, remoteFilename string, opts TransferOptions) error {
	method := opts.Method
	if method == "" {
		method = MethodAuto
	}
	hasRestAuth := opts.Password != "" || len(opts.PasswordBytes) > 0 || opts.ClientCertPath != "" || os.Getenv("BIGIP_PASSWORD") != "" || os.Getenv("BIGIQ_PASSWORD") != ""
	if method == MethodIControl {
		client := newClientFromOptions(host, opts, 443)
		defer client.Zero()
		return client.DeleteQKView(ctx, remoteFilename)
	}
	if method == MethodAuto && !isBigIQTarget(opts.TargetType) && opts.IdentityFile == "" && hasRestAuth {
		client := newClientFromOptions(host, opts, 443)
		defer client.Zero()
		if err := client.DeleteQKView(ctx, remoteFilename); err == nil {
			return nil
		} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return err
		}
	}

	scpOpts := newSCPOptions(host, opts)
	defer scpOpts.Zero()
	return DeleteQKViewSSH(ctx, scpOpts, remoteFilename)
}
