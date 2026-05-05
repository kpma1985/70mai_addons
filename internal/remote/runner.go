package remote

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"addon_installer/internal/config"
)

type Runner struct {
	Cfg config.Config
}

func (r *Runner) Run(ctx context.Context, shell string) (stdout string, stderr string, err error) {
	switch strings.ToLower(strings.TrimSpace(r.Cfg.Transport)) {
	case "http":
		return "", "", fmt.Errorf("transport http is removed; use ssh or telnet")
	case "telnet":
		return r.runTelnet(ctx, shell)
	default:
		return r.runSSH(ctx, shell)
	}
}

// Test checks whether the selected connection is reachable (short ping command or HTTP GET).
func (r *Runner) Test(ctx context.Context) error {
	switch strings.ToLower(strings.TrimSpace(r.Cfg.Transport)) {
	case "http":
		return fmt.Errorf("transport http is removed; use ssh or telnet")
	case "telnet":
		out, _, err := r.Run(ctx, "echo ADDON_TEST_OK")
		if err != nil {
			return err
		}
		if !strings.Contains(out, "ADDON_TEST_OK") {
			return fmt.Errorf("unerwartete Antwort: %q", strings.TrimSpace(out))
		}
		return nil
	default:
		out, stderr, err := r.Run(ctx, "echo ADDON_TEST_OK")
		if err != nil {
			if strings.TrimSpace(stderr) != "" {
				return fmt.Errorf("%w (%s)", err, strings.TrimSpace(stderr))
			}
			return err
		}
		if !strings.Contains(out, "ADDON_TEST_OK") {
			return fmt.Errorf("unerwartete Antwort: %q", strings.TrimSpace(out))
		}
		return nil
	}
}

func (r *Runner) AddressSSH() string {
	p := r.Cfg.SSHPort
	if p == 0 {
		p = 22
	}
	return fmt.Sprintf("%s:%d", r.Cfg.Host, p)
}

func (r *Runner) sshClientConfig() *ssh.ClientConfig {
	conf := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password(r.Cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Dashcam im LAN
		Timeout:         25 * time.Second,
	}
	if r.Cfg.Password == "" {
		conf.Auth = []ssh.AuthMethod{
			ssh.Password(""),
			ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = ""
				}
				return answers, nil
			}),
		}
	}
	return conf
}

// IsSSH returns true for default/empty transport and explicit "ssh".
func IsSSHTransport(transport string) bool {
	t := strings.ToLower(strings.TrimSpace(transport))
	return t == "" || t == "ssh"
}

// PushTailscalePair streams two local files to /mnt/sd per SSH (kein wget auf der Kamera).
func (r *Runner) PushTailscalePair(ctx context.Context, localTailscale, localTailscaled string) error {
	if !IsSSHTransport(r.Cfg.Transport) {
		return fmt.Errorf("PushTailscalePair nur mit Transport ssh")
	}
	if err := r.uploadFileSSH(ctx, localTailscale, "/mnt/sd/tailscale"); err != nil {
		return err
	}
	return r.uploadFileSSH(ctx, localTailscaled, "/mnt/sd/tailscaled")
}

// PushWpalib transfers all files from the embedded bin/wpalib/ directory to /mnt/sd/wpalib/ on the camera.
func (r *Runner) PushWpalib(ctx context.Context, fsys embed.FS) error {
	if !IsSSHTransport(r.Cfg.Transport) {
		return fmt.Errorf("PushWpalib nur mit Transport ssh")
	}
	entries, err := fsys.ReadDir("bin/wpalib")
	if err != nil {
		return fmt.Errorf("wpalib payload ist in diesem Binary nicht eingebettet: %w", err)
	}
	// Zielverzeichnis anlegen
	if err := r.uploadBytesSSH(ctx, nil, "/mnt/sd/wpalib/.keep", false); err != nil {
		// mkdir -p via Kommando
		client, cerr := r.sshClient()
		if cerr != nil {
			return cerr
		}
		defer client.Close()
		sess, serr := client.NewSession()
		if serr != nil {
			return serr
		}
		_ = sess.Run("mkdir -p /mnt/sd/wpalib")
		sess.Close()
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fsys.ReadFile(path.Join("bin/wpalib", e.Name()))
		if err != nil {
			return fmt.Errorf("wpalib %s lesen: %w", e.Name(), err)
		}
		remote := "/mnt/sd/wpalib/" + e.Name()
		if err := r.uploadBytesSSH(ctx, data, remote, true); err != nil {
			return fmt.Errorf("upload %s: %w", e.Name(), err)
		}
	}
	return nil
}

func (r *Runner) sshClient() (*ssh.Client, error) {
	return ssh.Dial("tcp", r.AddressSSH(), r.sshClientConfig())
}

// UploadBytes schreibt Bytes per SSH nach remotePath (Dateirechte 0644 bzw. 0755).
func (r *Runner) UploadBytes(ctx context.Context, remotePath string, data []byte, executable bool) error {
	if !IsSSHTransport(r.Cfg.Transport) {
		return fmt.Errorf("UploadBytes nur mit transport ssh")
	}
	return r.uploadBytesSSH(ctx, data, remotePath, executable)
}

func (r *Runner) uploadBytesSSH(ctx context.Context, data []byte, remotePath string, executable bool) error {
	client, err := r.sshClient()
	if err != nil {
		return fmt.Errorf("ssh dial: %w", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	tmp := remotePath + ".new"
	chmod := "644"
	if executable {
		chmod = "755"
	}
	sh := fmt.Sprintf("cat >'%s' && chmod %s '%s' && mv -f '%s' '%s'", tmp, chmod, tmp, tmp, remotePath)
	sess.Stdin = bytes.NewReader(data)
	var errb bytes.Buffer
	sess.Stderr = &errb

	done := make(chan error, 1)
	go func() { done <- sess.Run(sh) }()
	select {
	case <-ctx.Done():
		_ = sess.Close()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			if es := strings.TrimSpace(errb.String()); es != "" {
				return fmt.Errorf("%w: %s", err, es)
			}
			return err
		}
	}
	return nil
}

func (r *Runner) uploadFileSSH(ctx context.Context, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	client, err := ssh.Dial("tcp", r.AddressSSH(), r.sshClientConfig())
	if err != nil {
		return fmt.Errorf("ssh dial: %w", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	tmp := remotePath + ".new"
	// Pfade fest (/mnt/sd/…) — keine Nutzerstrings.
	sh := fmt.Sprintf("cat >'%s' && chmod +x '%s' && mv -f '%s' '%s'", tmp, tmp, tmp, remotePath)
	sess.Stdin = f
	var errb bytes.Buffer
	sess.Stderr = &errb

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(sh)
	}()

	select {
	case <-ctx.Done():
		_ = sess.Close()
		return ctx.Err()
	case err := <-done:
		if err != nil {
			es := strings.TrimSpace(errb.String())
			if es != "" {
				return fmt.Errorf("%w: %s", err, es)
			}
			return err
		}
		return nil
	}
}

func (r *Runner) runSSH(ctx context.Context, shell string) (string, string, error) {
	client, err := ssh.Dial("tcp", r.AddressSSH(), r.sshClientConfig())
	if err != nil {
		return "", "", fmt.Errorf("ssh dial: %w", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var outb, errb bytes.Buffer
	sess.Stdout = &outb
	sess.Stderr = &errb

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(shell)
	}()

	select {
	case <-ctx.Done():
		_ = sess.Close()
		return outb.String(), errb.String(), ctx.Err()
	case err := <-done:
		return outb.String(), errb.String(), err
	}
}

// RunScript executes a multi-line shell script via SSH stdin (used for APApply etc.).
func (r *Runner) RunScript(ctx context.Context, script string) (stdout, stderr string, err error) {
	if strings.EqualFold(strings.TrimSpace(r.Cfg.Transport), "telnet") {
		return "", "", fmt.Errorf("mehrzeilige Skripte: bitte Transport ssh oder http nutzen")
	}
	if strings.EqualFold(strings.TrimSpace(r.Cfg.Transport), "http") {
		return "", "", fmt.Errorf("RunScript nur mit ssh")
	}

	client, err := ssh.Dial("tcp", r.AddressSSH(), r.sshClientConfig())
	if err != nil {
		return "", "", fmt.Errorf("ssh dial: %w", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var outb, errb bytes.Buffer
	sess.Stdout = &outb
	sess.Stderr = &errb
	sess.Stdin = strings.NewReader(script)

	done := make(chan error, 1)
	go func() {
		done <- sess.Run("/bin/sh -s")
	}()

	select {
	case <-ctx.Done():
		_ = sess.Close()
		return outb.String(), errb.String(), ctx.Err()
	case err := <-done:
		return outb.String(), errb.String(), err
	}
}

func (r *Runner) runTelnet(ctx context.Context, shell string) (string, string, error) {
	p := r.Cfg.TelnetPort
	if p == 0 {
		p = 23
	}
	addr := fmt.Sprintf("%s:%d", r.Cfg.Host, p)
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", "", fmt.Errorf("tcp %s: %w", addr, err)
	}
	defer conn.Close()

	// tcpsh: eine Zeile Befehl, dann Antwort lesen
	if _, err := fmt.Fprintf(conn, "%s\n", strings.TrimSpace(shell)); err != nil {
		return "", "", err
	}

	time.Sleep(1500 * time.Millisecond)
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	out := string(buf[:n])
	if err != nil && err != io.EOF {
		return out, "", err
	}
	return out, "", nil
}
