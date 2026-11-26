// Command signsums signs and verifies release checksum files for the updater.
//
//	signsums keygen -out <private-key-file>   create a key pair (prints the public key)
//	signsums sign <SHA256SUMS>                 write <SHA256SUMS>.sig using $MAGNETAR_UPDATE_KEY
//	signsums verify <pubkey-base64> <SHA256SUMS>
//
// The private key is a base64-encoded Ed25519 seed. In CI it comes from the
// UPDATE_SIGNING_KEY repository secret and is never written to disk.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "signsums:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: signsums keygen -out FILE | sign FILE | verify PUBKEY FILE")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "file to write the private key to (mode 0600)")
	fs.Parse(args)
	if *out == "" {
		return errors.New("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(args []string) error {
	if len(args) != 1 {
		usage()
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("MAGNETAR_UPDATE_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("MAGNETAR_UPDATE_KEY must be a base64 Ed25519 seed")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
	return os.WriteFile(args[0]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644)
}

func verify(args []string) error {
	if len(args) != 2 {
		usage()
	}
	pub, err := base64.StdEncoding.DecodeString(args[0])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	sigB64, err := os.ReadFile(args[1] + ".sig")
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("signature is NOT valid")
	}
	fmt.Println("signature OK")
	return nil
}
