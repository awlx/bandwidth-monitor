package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBuildIndexHTMLWithProjectAssets(t *testing.T) {
	if _, err := BuildIndexHTML(os.DirFS("../static"), "test-version"); err != nil {
		t.Fatalf("BuildIndexHTML() with project assets error = %v", err)
	}
}

func TestBuildIndexHTMLFingerprintsAllLocalAssets(t *testing.T) {
	files := fstest.MapFS{
		"index.html": {
			Data: []byte(`<!doctype html><html><head>
<link rel="stylesheet" href="style.css">
<link rel="preconnect" href="https://cdn.example.com">
<script src="//cdn.example.com/library.js"></script>
<script src="https://cdn.example.com/other.js"></script>
<script src="js/tabs/example.js"></script>
</head><body>Bandwidth Monitor<span>v1.0</span></body></html>`),
		},
		"style.css":          {Data: []byte("body {}")},
		"js/tabs/example.js": {Data: []byte("window.example = true;")},
	}

	got, err := BuildIndexHTML(files, "test-version")
	if err != nil {
		t.Fatalf("BuildIndexHTML() error = %v", err)
	}
	html := string(got)

	for _, want := range []string{
		`href="style.css?v=` + shortHash(files["style.css"].Data) + `"`,
		`src="js/tabs/example.js?v=` + shortHash(files["js/tabs/example.js"].Data) + `"`,
		`href="https://cdn.example.com"`,
		`src="//cdn.example.com/library.js"`,
		`src="https://cdn.example.com/other.js"`,
		`Bandwidth Monitor<span>test-version</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rewritten index missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "cdn.example.com?") || strings.Contains(html, "cdn.example.com/library.js?") || strings.Contains(html, "cdn.example.com/other.js?") {
		t.Errorf("external URLs were fingerprinted:\n%s", html)
	}
}

func TestBuildIndexHTMLAssetVersionChangesWithContent(t *testing.T) {
	index := []byte(`<script src="js/tabs/example.js"></script>`)
	first := fstest.MapFS{
		"index.html":         {Data: index},
		"js/tabs/example.js": {Data: []byte("first")},
	}
	second := fstest.MapFS{
		"index.html":         {Data: index},
		"js/tabs/example.js": {Data: []byte("second")},
	}

	firstHTML, err := BuildIndexHTML(first, "test")
	if err != nil {
		t.Fatalf("first BuildIndexHTML() error = %v", err)
	}
	secondHTML, err := BuildIndexHTML(second, "test")
	if err != nil {
		t.Fatalf("second BuildIndexHTML() error = %v", err)
	}
	if string(firstHTML) == string(secondHTML) {
		t.Fatalf("asset content change did not change rewritten index: %s", firstHTML)
	}
}

func TestBuildIndexHTMLRejectsMissingLocalAsset(t *testing.T) {
	files := fstest.MapFS{
		"index.html": {Data: []byte(`<script src="js/missing.js"></script>`)},
	}

	_, err := BuildIndexHTML(files, "test")
	if err == nil || !strings.Contains(err.Error(), `read index asset "js/missing.js"`) {
		t.Fatalf("BuildIndexHTML() error = %v, want missing asset error", err)
	}
}

func shortHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:4])
}
