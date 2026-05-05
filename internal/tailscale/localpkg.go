package tailscale

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultVer = "1.96.4"
const fallbackVer = "1.80.0"

func pkgsURL(ver string) string {
	return "https://pkgs.tailscale.com/stable/tailscale_" + ver + "_arm64.tgz"
}

// CacheDir returns e.g. ~/.cache/x800_addon/tailscale (macOS/Linux) or OS-equivalent.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "x800_addon", "tailscale"), nil
}

// PrepareBinaries downloads the linux/arm64 tarball (cached under cacheRoot) and extracts
// tailscale + tailscaled into a fresh staging directory. Caller must RemoveAll(stagingDir) when done.
// tsURL overrides pkgs URLs when non-empty. tsVer selects pkgs version when tsURL is empty (default DefaultVer).
func PrepareBinaries(ctx context.Context, cacheRoot string, tsURL, tsVer string) (tailscaleBin, tailscaledBin, stagingDir string, err error) {
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		return "", "", "", err
	}
	stagingDir, err = os.MkdirTemp(cacheRoot, "extract_")
	if err != nil {
		return "", "", "", err
	}
	cleanupStaging := func() { _ = os.RemoveAll(stagingDir) }

	tgzPath, err := resolveTarball(ctx, cacheRoot, tsURL, tsVer)
	if err != nil {
		cleanupStaging()
		return "", "", "", err
	}

	if err := extractPair(ctx, tgzPath, stagingDir); err != nil {
		cleanupStaging()
		return "", "", "", err
	}
	ts := filepath.Join(stagingDir, "tailscale")
	td := filepath.Join(stagingDir, "tailscaled")
	if _, err := os.Stat(ts); err != nil {
		cleanupStaging()
		return "", "", "", fmt.Errorf("tailscale binary fehlt nach extract: %w", err)
	}
	if _, err := os.Stat(td); err != nil {
		cleanupStaging()
		return "", "", "", fmt.Errorf("tailscaled binary fehlt nach extract: %w", err)
	}
	return ts, td, stagingDir, nil
}

func resolveTarball(ctx context.Context, cacheRoot, tsURL, tsVer string) (string, error) {
	tsURL = strings.TrimSpace(tsURL)
	if tsURL != "" {
		name := filepath.Base(tsURL)
		if name == "/" || name == "." || name == "" {
			name = "custom.tgz"
		}
		dst := filepath.Join(cacheRoot, name)
		if err := downloadFile(ctx, tsURL, dst); err != nil {
			return "", err
		}
		return dst, nil
	}

	ver := strings.TrimSpace(tsVer)
	if ver == "" {
		ver = DefaultVer
	}
	primary := filepath.Join(cacheRoot, fmt.Sprintf("tailscale_%s_arm64.tgz", ver))
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	}
	if err := downloadFile(ctx, pkgsURL(ver), primary); err == nil {
		return primary, nil
	}
	_ = os.Remove(primary)

	fb := filepath.Join(cacheRoot, fmt.Sprintf("tailscale_%s_arm64.tgz", fallbackVer))
	if _, err := os.Stat(fb); err == nil {
		return fb, nil
	}
	if err := downloadFile(ctx, pkgsURL(fallbackVer), fb); err != nil {
		return "", fmt.Errorf("pkgs download %s und Fallback %s: %w", pkgsURL(ver), pkgsURL(fallbackVer), err)
	}
	return fb, nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d für %s", res.StatusCode, url)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func extractPair(ctx context.Context, tgzPath, destDir string) error {
	f, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var haveTS, haveTSD bool

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		base := filepath.Base(hdr.Name)
		var outName string
		switch base {
		case "tailscale":
			outName = "tailscale"
			haveTS = true
		case "tailscaled":
			outName = "tailscaled"
			haveTSD = true
		default:
			continue
		}
		dst := filepath.Join(destDir, outName)
		mode := fsMode(hdr)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	if !haveTS || !haveTSD {
		return fmt.Errorf("im Tarball fehlen tailscale/tailscaled (linux/arm64)")
	}
	return nil
}

func fsMode(hdr *tar.Header) os.FileMode {
	if hdr.Mode != 0 {
		return os.FileMode(hdr.Mode) & 0777
	}
	return 0755
}
