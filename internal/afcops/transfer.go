package afcops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"carriersim/internal/host"
	"carriersim/internal/run"
	"carriersim/internal/tree"

	"github.com/danielpaulus/go-ios/ios"
)

type TransferOpts struct {
	Entry      ios.DeviceEntry
	RunDir     string
	Payload    tree.Tree // nil = export only
	Expected   tree.Tree // optional expected export
	Recovery   bool
	ExePath    string
	AppleDirs  []string
}

func Transfer(opts TransferOpts) (tree.Tree, error) {
	if err := os.Mkdir(opts.RunDir, 0o700); err != nil {
		return nil, err
	}
	token := make([]byte, 10)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	tok := hex.EncodeToString(token)
	source := "airlift-src-" + tok
	link := "airlift-link-" + tok
	exported := "airlift-saved-" + tok
	finalSource := exported
	if opts.Payload != nil {
		finalSource = source + "/" + tree.PayloadPath
	}
	targetRel := strings.TrimPrefix(tree.Target, "/var/mobile/")
	assets := [][2]string{
		{"../../" + source + "/p0/p1/p2/link", link},
		{"../../../" + targetRel, exported},
		{"../../" + finalSource, link + "/iPhone"},
	}
	journal := map[string]interface{}{
		"schema":       1,
		"udid_hash":    tree.Digest([]byte(opts.Entry.Properties.SerialNumber)),
		"target":       tree.Target,
		"source":       source,
		"link":         link,
		"exported":     exported,
		"complete":     false,
		"phase":        "created",
		"payload_hash": nil,
	}
	if opts.Payload != nil {
		journal["payload_hash"] = tree.Hash(opts.Payload)
	}
	phase := func(name string, extra map[string]interface{}) error {
		journal["phase"] = name
		for k, v := range extra {
			journal[k] = v
		}
		return run.SaveJSON(filepath.Join(opts.RunDir, "journal.json"), journal)
	}
	if err := phase("created", nil); err != nil {
		return nil, err
	}

	c, err := Open(opts.Entry)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	for _, p := range []string{source, link, exported} {
		if _, ok, err := c.Exists(p); err != nil {
			return nil, err
		} else if ok {
			return nil, fmt.Errorf("Staging path collision")
		}
	}
	books, booksExisted, err := c.BooksSnapshot(opts.RunDir)
	if err != nil {
		return nil, err
	}
	mutated := false
	var snapshot tree.Tree
	defer func() {
		if mutated {
			if err := c.RestoreBooks(books, booksExisted); err != nil {
				journal["books_restored"] = false
				journal["books_restore_error"] = err.Error()
				_ = run.SaveJSON(filepath.Join(opts.RunDir, "journal.json"), journal)
			} else {
				journal["books_restored"] = true
				_ = run.SaveJSON(filepath.Join(opts.RunDir, "journal.json"), journal)
			}
		}
	}()

	raw, err := tree.StagingArchive(opts.Payload)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opts.RunDir, "staging.zip"), raw, 0o600); err != nil {
		return nil, err
	}
	if opts.Payload != nil {
		if err := tree.WriteZip(filepath.Join(opts.RunDir, "desired.zip"), opts.Payload); err != nil {
			return nil, err
		}
	}
	if err := phase("staging", map[string]interface{}{"requires_recovery": true}); err != nil {
		return nil, err
	}
	mutated = true
	if err := StreamZip(opts.Entry, source, raw); err != nil {
		journal["operation_error"] = err.Error()
		_ = run.SaveJSON(filepath.Join(opts.RunDir, "journal.json"), journal)
		return nil, err
	}
	node, ok, err := c.Exists(source + "/p0/p1/p2/link")
	if err != nil {
		return nil, err
	}
	wantLink := "../../../" + tree.Parent[1:]
	if !ok || !node.IsLink() || node.LinkTarget != wantLink {
		return nil, fmt.Errorf("Staged link mismatch")
	}
	if opts.Payload != nil {
		staged, err := c.RemoteTree(source + "/" + tree.PayloadPath)
		if err != nil {
			return nil, err
		}
		if !tree.Equal(staged, opts.Payload) {
			return nil, fmt.Errorf("Staged carrier tree mismatch")
		}
	}
	if err := c.MkDirs("Books/Sync"); err != nil {
		return nil, err
	}
	meta, err := BuildBooksMetadata(assets)
	if err != nil {
		return nil, err
	}
	if err := c.SetFile("Books/Sync/Books.plist", meta); err != nil {
		return nil, err
	}
	got, err := c.GetFile("Books/Sync/Books.plist")
	if err != nil || string(got) != string(meta) {
		return nil, fmt.Errorf("Books staging mismatch")
	}

	pause := func() error {
		if err := phase("export-check", nil); err != nil {
			return err
		}
		for i := 0; i < 40; i++ {
			if _, ok, _ := c.Exists(exported); ok {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		node, ok, err := c.Exists(exported)
		if err != nil {
			return err
		}
		if !ok && opts.Recovery && opts.Payload != nil {
			return phase("recovery-final-authorized", nil)
		}
		if !ok || !node.IsDir() {
			return fmt.Errorf("No exported catalog. Do not retry blindly; inspect journal and original paths.")
		}
		if err := phase("original-exported", nil); err != nil {
			return err
		}
		snapshot, err = c.RemoteTree(exported)
		if err != nil {
			return err
		}
		if err := tree.WriteZip(filepath.Join(opts.RunDir, "original.zip"), snapshot); err != nil {
			return err
		}
		if err := phase("backup-saved", map[string]interface{}{"original_hash": tree.Hash(snapshot)}); err != nil {
			return err
		}
		if opts.Expected != nil && !tree.Equal(snapshot, opts.Expected) {
			return fmt.Errorf("Carrier catalog changed since snapshot; recover this run")
		}
		check, err := c.RemoteTree(exported)
		if err != nil {
			return err
		}
		if !tree.Equal(check, snapshot) {
			return fmt.Errorf("Export changed after backup")
		}
		return phase("final-authorized", nil)
	}

	if err := phase("host-started", nil); err != nil {
		return nil, err
	}
	assetPairs := make([]host.Asset, len(assets))
	for i, a := range assets {
		assetPairs[i] = host.Asset{ID: a[0], Dest: a[1]}
	}
	if err := host.Session(opts.ExePath, opts.Entry.Properties.SerialNumber, assetPairs, opts.AppleDirs, opts.RunDir, pause); err != nil {
		journal["operation_error"] = err.Error()
		_ = run.SaveJSON(filepath.Join(opts.RunDir, "journal.json"), journal)
		return nil, err
	}
	for i := 0; i < 30; i++ {
		if _, ok, _ := c.Exists(finalSource); !ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, ok, _ := c.Exists(finalSource); ok {
		return nil, fmt.Errorf("Final source not consumed; operation unconfirmed")
	}
	if err := phase("placement-observed", map[string]interface{}{"complete": true, "requires_recovery": false}); err != nil {
		return nil, err
	}
	return snapshot, nil
}
