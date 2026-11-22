package web

import (
	"strings"
	"testing"
)

func TestManifestCoversEmbeddedAssets(t *testing.T) {
	files, listing, total, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "app.js", "crypto.js", "keystore.js", "linkify.js", "style.css", "favicon.svg"} {
		if len(files[name]) != 64 {
			t.Errorf("%s: missing or malformed hash %q", name, files[name])
		}
	}
	for name, hash := range files {
		if !strings.Contains(listing, hash+"  "+name+"\n") {
			t.Errorf("listing lacks %s", name)
		}
	}
	_, _, again, _ := Manifest()
	if len(total) != 64 || total != again || strings.ToLower(total) != total {
		t.Fatal("manifest hash is not a stable lowercase SHA-256")
	}
}
