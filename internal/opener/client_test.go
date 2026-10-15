package opener

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuestFilesResolveInSharedNamespace(t *testing.T) {
	root := project(t)
	t.Chdir(root)
	file := filepath.Join(root, "file with spaces.txt")
	fileURI := (&url.URL{Scheme: "file", Path: file}).String()

	for _, target := range []string{"file with spaces.txt", file, fileURI} {
		got, err := guestURI(target, root)
		if err != nil || got != "file:///project/file%20with%20spaces.txt" {
			t.Fatalf("target %q: got %q, %v", target, got, err)
		}
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"", "https://example.com/\n", "file://remote/project/file", "file:relative", fileURI + "?query=1", "missing", "escape/secret", filepath.Join(outside, "secret")} {
		if uri, err := guestURI(target, root); err == nil {
			t.Fatalf("accepted %q as %q", target, uri)
		}
	}
}

func TestGuestClientTalksToHostProtocol(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	server.SetDeadline(time.Now().Add(time.Second))

	target := "https://example.com/?value=$(not-a-command)"
	root := t.TempDir()
	launched := make(chan string, 1)
	go func() {
		response := Handle(server, 1000, 1000, root, func(uri string) error {
			launched <- uri
			return nil
		})
		json.NewEncoder(server).Encode(response)
	}()

	if err := exchange(client, target); err != nil {
		t.Fatal(err)
	}
	if got := <-launched; got != target {
		t.Fatalf("changed target to %q", got)
	}
}

type responseStream struct {
	*strings.Reader
	written bytes.Buffer
}

func (s *responseStream) Write(data []byte) (int, error) {
	return s.written.Write(data)
}

func TestGuestClientRejectsInvalidAndFailedResponses(t *testing.T) {
	for _, response := range []string{
		`{"ok":false,"error":"denied"}` + "\n", "[]\n", "null\n", "{}\n",
		`{"ok":"true"}` + "\n", `{"ok":true}`, strings.Repeat("x", MaxRequest+1) + "\n",
	} {
		stream := &responseStream{Reader: strings.NewReader(response)}
		if err := exchange(stream, "https://example.com"); err == nil {
			t.Fatalf("accepted invalid response %.60q", response)
		}
	}

	stream := &responseStream{Reader: strings.NewReader("{\"ok\":true}\n")}
	if err := exchange(stream, strings.Repeat("x", MaxRequest)); err == nil || stream.written.Len() != 0 {
		t.Fatal("oversized request was sent")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Open(ctx, "https://example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
