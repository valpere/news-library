package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// curl's browser-compatible transport is an explicit fallback for recognized
// interstitials. Every connection, including redirects, uses the guarded proxy.
func (f *Fetcher) browserGet(ctx context.Context, raw string) ([]byte, *url.URL, string, bool, error) {
	if _, err := CanonicalURL(raw); err != nil {
		return nil, nil, "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	proxy, closeProxy, err := startMetadataProxy(ctx)
	if err != nil {
		return nil, nil, "", false, err
	}
	defer closeProxy()
	agent := f.UserAgent
	if agent == "" {
		agent = DefaultSourceUserAgent
	}
	cmd := exec.CommandContext(ctx, f.CURL, "-q", "--silent", "--show-error", "--location", "--max-redirs", "5", "--max-time", "24", "--connect-timeout", "8", "--proxy", proxy, "--noproxy", "", "--proto", "=http,https", "--proto-redir", "=http,https", "--user-agent", agent, "--max-filesize", strconv.FormatInt(f.MaxBytes, 10), "--write-out", "\nNEWS_LIBRARY_HTTP:%{json}", "--url", raw)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out := &cappedOutput{limit: int(f.MaxBytes) + (64 << 10)}
	cmd.Stdout = out
	stderr := &cappedOutput{limit: 4096}
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		return nil, nil, "", false, &SourceError{Kind: "source", Message: "браузерний запит джерела не завершено; перевірте доступ або вставте текст вручну"}
	}
	marker := []byte("\nNEWS_LIBRARY_HTTP:")
	pos := bytes.LastIndex(out.Bytes(), marker)
	if pos < 0 {
		return nil, nil, "", false, errors.New("missing browser response status")
	}
	var response struct {
		Status      int    `json:"http_code"`
		URL         string `json:"url_effective"`
		ContentType string `json:"content_type"`
	}
	if err = json.Unmarshal(out.Bytes()[pos+len(marker):], &response); err != nil {
		return nil, nil, "", false, errors.New("invalid browser response metadata")
	}
	status := response.Status
	if status != 200 && status != 206 {
		return nil, nil, "", false, &SourceError{Message: fmt.Sprintf("джерело повернуло HTTP %d", status), Retry: status == 429 || status >= 500}
	}
	if _, err = CanonicalURL(response.URL); err != nil {
		return nil, nil, "", false, err
	}
	final, err := url.Parse(response.URL)
	if err != nil {
		return nil, nil, "", false, err
	}
	body := out.Bytes()[:pos]
	partial := status == 206 || int64(len(body)) > f.MaxBytes
	if int64(len(body)) > f.MaxBytes {
		body = body[:f.MaxBytes]
	}
	return body, final, response.ContentType, partial, nil
}
