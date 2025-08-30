package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	var key string
	flag.StringVar(&key, "key", "", "Raw API key to hash with SHA-256")
	flag.Parse()
	if key == "" {
		fmt.Fprintln(os.Stderr, "usage: hash-key -key <RAW_KEY>")
		os.Exit(1)
	}
	sum := sha256.Sum256([]byte(key))
	fmt.Printf("%s\n", hex.EncodeToString(sum[:]))
}
