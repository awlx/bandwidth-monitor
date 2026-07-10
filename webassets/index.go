// Package webassets prepares embedded web assets for serving.
package webassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// BuildIndexHTML fingerprints every local script and link asset referenced by
// index.html, then injects the build version displayed in the header.
func BuildIndexHTML(staticFS fs.FS, buildVersion string) ([]byte, error) {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}

	data, err = fingerprintIndexAssets(staticFS, data)
	if err != nil {
		return nil, err
	}

	data = bytes.Replace(data, []byte("Bandwidth Monitor<span>v1.0</span>"), []byte("Bandwidth Monitor<span>"+buildVersion+"</span>"), 1)
	return data, nil
}

func fingerprintIndexAssets(staticFS fs.FS, index []byte) ([]byte, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(index))
	var output bytes.Buffer

	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case html.ErrorToken:
			if tokenizer.Err() != io.EOF {
				return nil, fmt.Errorf("parse index.html: %w", tokenizer.Err())
			}
			return output.Bytes(), nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			attribute := ""
			switch strings.ToLower(token.Data) {
			case "script":
				attribute = "src"
			case "link":
				attribute = "href"
			}
			if attribute == "" {
				output.Write(tokenizer.Raw())
				continue
			}

			changed := false
			for i := range token.Attr {
				if !strings.EqualFold(token.Attr[i].Key, attribute) {
					continue
				}
				versioned, local, err := fingerprintAssetURL(staticFS, token.Attr[i].Val)
				if err != nil {
					return nil, err
				}
				if local {
					token.Attr[i].Val = versioned
					changed = true
				}
			}
			if changed {
				output.WriteString(token.String())
			} else {
				output.Write(tokenizer.Raw())
			}
		default:
			output.Write(tokenizer.Raw())
		}
	}
}

func fingerprintAssetURL(staticFS fs.FS, rawURL string) (string, bool, error) {
	assetURL, err := url.Parse(rawURL)
	if err != nil {
		return "", false, fmt.Errorf("parse asset URL %q: %w", rawURL, err)
	}
	if assetURL.Scheme != "" || assetURL.Host != "" || strings.HasPrefix(rawURL, "//") || assetURL.Path == "" {
		return rawURL, false, nil
	}

	assetPath := strings.TrimPrefix(path.Clean("/"+assetURL.Path), "/")
	data, err := fs.ReadFile(staticFS, assetPath)
	if err != nil {
		return "", false, fmt.Errorf("read index asset %q: %w", assetPath, err)
	}
	hash := sha256.Sum256(data)
	query := assetURL.Query()
	query.Set("v", hex.EncodeToString(hash[:4]))
	assetURL.RawQuery = query.Encode()
	return assetURL.String(), true, nil
}
