package tree

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	MaxBytes = 64 * 1024 * 1024
	MaxNodes = 4000
)

// Node is a file-system tree entry: kind is "d", "f", or "l".
type Node struct {
	Kind string
	Data []byte
}

type Tree map[string]Node

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func Require(ok bool, message string) error {
	if !ok {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func SafeName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("Unsafe tree path: %s", name)
	}
	for _, p := range strings.Split(name, "/") {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("Unsafe tree path: %s", name)
		}
	}
	return nil
}

func Validate(t Tree) error {
	if len(t) > MaxNodes {
		return fmt.Errorf("Too many tree nodes")
	}
	total := 0
	for name, n := range t {
		total += len(n.Data)
		if err := SafeName(name); err != nil {
			return err
		}
		if n.Kind != "d" && n.Kind != "f" && n.Kind != "l" {
			return fmt.Errorf("Unknown node type")
		}
		for parent := path.Dir(name); parent != "." && parent != "/"; parent = path.Dir(parent) {
			pn, ok := t[parent]
			if !ok || pn.Kind != "d" {
				return fmt.Errorf("Missing or non-directory parent")
			}
		}
		if n.Kind == "l" {
			if bytes.Contains(n.Data, []byte{0}) || len(n.Data) > 4096 {
				return fmt.Errorf("Invalid symlink")
			}
		}
	}
	if total > MaxBytes {
		return fmt.Errorf("Tree too large")
	}
	return nil
}

func Hash(t Tree) string {
	// Match Python: json.dumps({n: [k, digest(b)] ...}, sort_keys=True)
	type pyPair []interface{}
	out := make(map[string]pyPair, len(t))
	for n, v := range t {
		out[n] = pyPair{v.Kind, Digest(v.Data)}
	}
	raw, _ := json.Marshal(out)
	return Digest(raw)
}

func Equal(a, b Tree) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va.Kind != vb.Kind || !bytes.Equal(va.Data, vb.Data) {
			return false
		}
	}
	return true
}

func WriteZip(path string, t Tree) error {
	raw, err := WriteZipBytes(t, false)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(raw)
	return err
}

func ReadZip(path string) (Tree, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if len(r.File) > MaxNodes {
		return nil, fmt.Errorf("Archive too large")
	}
	var total uint64
	for _, f := range r.File {
		total += f.UncompressedSize64
	}
	if total > MaxBytes {
		return nil, fmt.Errorf("Archive too large")
	}
	t := make(Tree)
	for _, f := range r.File {
		name := strings.TrimSuffix(f.Name, "/")
		if err := SafeName(name); err != nil {
			return nil, err
		}
		if _, exists := t[name]; exists {
			return nil, fmt.Errorf("Duplicate archive entry")
		}
		mode := os.FileMode(f.Mode())
		kind := "f"
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			kind = "d"
		} else if mode&os.ModeSymlink != 0 {
			kind = "l"
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		t[name] = Node{Kind: kind, Data: data}
	}
	if err := Validate(t); err != nil {
		return nil, err
	}
	return t, nil
}

