package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const githubRepo = "itsmostafa/typesafe-mcp"

const (
	// One deadline covers connect, headers and body: a stalled mirror must not
	// hang `evaluate update` forever.
	updateTimeout = 5 * time.Minute
	// Release JSON and SHA256SUMS.txt are a few KB; the archive is one
	// compressed binary. Both caps are generous by orders of magnitude.
	maxMetadataBytes = 1 << 20
	maxArchiveBytes  = 100 << 20
)

var updateClient = &http.Client{Timeout: updateTimeout}

// httpGet fetches url with the command's context and a bounded client, and
// rejects non-200 responses so no caller parses an error page as data.
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return resp, nil
}

// download reads url into memory, failing if the body exceeds limit bytes.
func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("%s exceeds %d bytes", url, limit)
	}
	return b, err
}

// runUpdate replaces the running binary with the latest GitHub release.
func runUpdate(ctx context.Context) error {
	exe, err := os.Executable()
	if err == nil {
		// Depending on the OS, exe can be the symlink the binary was started
		// through; renaming over it would replace the link, not the binary.
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}

	// Stage beside exe so the final rename never crosses filesystems, and an
	// unwritable install dir fails before anything is downloaded. CreateTemp's
	// exclusive random name can't be redirected by a planted symlink.
	staged, err := os.CreateTemp(filepath.Dir(exe), ".evaluate-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%s is not writable, re-run with: sudo evaluate update", filepath.Dir(exe))
		}
		return fmt.Errorf("staging update: %w", err)
	}
	defer os.Remove(staged.Name()) // no-op after a successful rename
	defer staged.Close()

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("fetching latest release: %w", err)
	}
	// ponytail: equality, not semver; a local build newer than the latest
	// release gets replaced by it. Compare versions if that starts to bite.
	if version == release.TagName {
		fmt.Printf("Already up to date (%s).\n", version)
		return nil
	}
	fmt.Printf("Updating %s → %s\n", version, release.TagName)

	archiveName := fmt.Sprintf("evaluate-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archiveURL, err := findAssetURL(release.Assets, archiveName)
	if err != nil {
		return err
	}
	sumsURL, err := findAssetURL(release.Assets, "SHA256SUMS.txt")
	if err != nil {
		return err
	}
	expectedHash, err := fetchExpectedChecksum(ctx, sumsURL, archiveName)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}

	archive, err := download(ctx, archiveURL, maxArchiveBytes)
	if err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != expectedHash {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, expectedHash)
	}

	if err := extractBinaryFromTar(archive, "evaluate", staged); err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Chmod(staged.Name(), 0755); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), exe); err != nil {
		return fmt.Errorf("replacing executable: %w", err)
	}

	fmt.Printf("Updated to %s. Run `evaluate version` to confirm.\n", release.TagName)
	return nil
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	body, err := download(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo), maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	var rel githubRelease
	return &rel, json.Unmarshal(body, &rel)
}

func findAssetURL(assets []githubAsset, name string) (string, error) {
	for _, a := range assets {
		if a.Name == name {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("asset %q not found in release", name)
}

func fetchExpectedChecksum(ctx context.Context, sumsURL, assetName string) (string, error) {
	body, err := download(ctx, sumsURL, maxMetadataBytes)
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(body)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum found for %q", assetName)
}

// extractBinaryFromTar copies the regular file binaryName from a .tar.gz
// archive into dst.
func extractBinaryFromTar(archive []byte, binaryName string, dst io.Writer) error {
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name != binaryName {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive member %q is not a regular file", hdr.Name)
		}
		// tar.Reader stops at hdr.Size, so this bounds the copy too.
		if hdr.Size > maxArchiveBytes {
			return fmt.Errorf("archive member %q declares %d bytes, over the %d byte limit", hdr.Name, hdr.Size, int64(maxArchiveBytes))
		}
		_, err = io.Copy(dst, tr)
		return err
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
}
