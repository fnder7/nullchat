// Command webhash prints SHA-256 hashes of the embedded frontend in sha256sum
// format plus a manifest digest, to compare a deployment with this source tree.
package main

import (
	"fmt"
	"os"

	"nullchat/web"
)

func main() {
	_, listing, total, err := web.Manifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(listing)
	fmt.Printf("manifest %s\n", total)
}
