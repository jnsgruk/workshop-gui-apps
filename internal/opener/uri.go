package opener

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var uriSchemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// Target translates file URIs through the shared /project namespace.
func Target(uri, project string) (string, error) {
	if uri == "" || hasControlCharacters(uri) || !uriSchemePattern.MatchString(uri) {
		return "", fmt.Errorf("expected an absolute URI without control characters")
	}

	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	if u.Scheme != "file" {
		return uri, nil
	}

	if err := validateLocalFileURI(u); err != nil {
		return "", err
	}

	relative, err := filepath.Rel("/project", u.Path)
	if err != nil || escapesRoot(relative) {
		return "", fmt.Errorf("only files shared under /project can open on the host")
	}

	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return "", err
	}

	project, err = filepath.Abs(project)
	if err != nil {
		return "", err
	}

	target, err := filepath.EvalSymlinks(filepath.Join(project, relative))
	if err != nil {
		return "", err
	}

	relative, err = filepath.Rel(project, target)
	if err != nil || escapesRoot(relative) {
		return "", fmt.Errorf("file resolves outside the shared project")
	}

	return (&url.URL{Scheme: "file", Path: target}).String(), nil
}

// GuestURI normalizes a URL or existing guest file to the host protocol.
func GuestURI(target string) (string, error) {
	return guestURI(target, "/project")
}

func guestURI(target, sharedRoot string) (string, error) {
	if target == "" || hasControlCharacters(target) {
		return "", fmt.Errorf("expected a target without control characters")
	}

	if uriSchemePattern.MatchString(target) {
		u, err := url.Parse(target)
		if err != nil {
			return "", err
		}

		if u.Scheme != "file" {
			return target, nil
		}

		if err := validateLocalFileURI(u); err != nil {
			return "", err
		}

		target = u.Path
	}

	path, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}

	sharedRoot, err = filepath.EvalSymlinks(sharedRoot)
	if err != nil {
		return "", err
	}

	relative, err := filepath.Rel(sharedRoot, path)
	if err != nil || escapesRoot(relative) {
		return "", fmt.Errorf("only files shared under /project can open on the host")
	}

	return (&url.URL{Scheme: "file", Path: filepath.Join("/project", relative)}).String(), nil
}

func escapesRoot(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateLocalFileURI(u *url.URL) error {
	if (u.Host != "" && u.Host != "localhost") || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("expected a local file URI without query or fragment")
	}

	if !filepath.IsAbs(u.Path) {
		return fmt.Errorf("expected an absolute file path")
	}

	return nil
}

func hasControlCharacters(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool {
		return r < 32 || r == 127
	})
}
