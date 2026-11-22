package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
)

//go:embed index.html app.js crypto.js keystore.js linkify.js style.css trust.js invite.js favicon.svg
var assets embed.FS

func Handler() http.Handler { return http.FileServer(http.FS(assets)) }

// Manifest returns the SHA-256 of every embedded asset, the same data as
// "hash  name" lines in sha256sum format (sorted by name), and the SHA-256 of
// that listing, so a deployment can be compared with a published release.
func Manifest() (files map[string]string, listing, total string, err error) {
	files = map[string]string{}
	err = fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines strings.Builder
	for _, name := range names {
		fmt.Fprintf(&lines, "%s  %s\n", files[name], name)
	}
	sum := sha256.Sum256([]byte(lines.String()))
	return files, lines.String(), hex.EncodeToString(sum[:]), nil
}
