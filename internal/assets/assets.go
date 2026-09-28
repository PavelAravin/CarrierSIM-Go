package assets

import (
	"fmt"
	"os"
	"path/filepath"

	"carriersim/internal/tree"
)

const AssetSHA256 = "6de1ea0be81a29c145ef414f24bc21d1dcb8a4eb737b22b1f956e9a6f0c2098b"

// Load reads assets.zip. bundle is the target carrier bundle on the phone
// (empty = Vodafone_hu.bundle).
func Load(root, bundle string) (bundles tree.Tree, assets tree.Tree, err error) {
	name, err := tree.NormalizeBundle(bundle)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(root, "assets.zip")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if tree.Digest(raw) != AssetSHA256 {
		return nil, nil, fmt.Errorf("Архив assets.zip повреждён или заменён.")
	}
	assets, err = tree.ReadZip(path)
	if err != nil {
		return nil, nil, err
	}
	bundles = tree.Tree{name: tree.SystemLink(name)}
	return bundles, assets, nil
}
