package tree

import (
	"fmt"
	"regexp"
	"strings"

	"howett.net/plist"
)

const (
	Parent      = "/var/mobile/Library/Carrier Bundles"
	Target      = Parent + "/iPhone"
	PayloadPath = "q0/q1/q2/q3/q4/payload"
	Bundle      = "Vodafone_hu.bundle"
)

var (
	TargetBundles = []string{"Vodafone_hu.bundle"}
	SystemPrefix  = "../../../../../../System/Library/Carrier Bundles/iPhone/"
	reBundleName  = regexp.MustCompile(`^[A-Za-z0-9_]+\.bundle$`)
)

// NormalizeBundle validates and returns a carrier bundle file name (e.g. Swisscom_ch.bundle).
func NormalizeBundle(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"'`)
	if name == "" {
		return Bundle, nil
	}
	if !strings.HasSuffix(strings.ToLower(name), ".bundle") {
		name += ".bundle"
	}
	if !reBundleName.MatchString(name) {
		return "", fmt.Errorf("Неверное имя бандла %q. Пример: Vodafone_hu.bundle или Swisscom_ch", name)
	}
	return name, nil
}

func SystemLink(name string) Node {
	return Node{Kind: "l", Data: []byte(SystemPrefix + name)}
}

func BundleInfo(t Tree, name string) (map[string]interface{}, map[string]interface{}, error) {
	if name == "" {
		name = Bundle
	}
	prefix := name + "/"
	pl := func(file string) (map[string]interface{}, error) {
		v, ok := t[prefix+file]
		if !ok || v.Kind != "f" {
			return nil, fmt.Errorf("Missing %s%s", prefix, file)
		}
		var out map[string]interface{}
		if _, err := plist.Unmarshal(v.Data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	info, err := pl("Info.plist")
	if err != nil {
		return nil, nil, err
	}
	carrier, err := pl("carrier.plist")
	if err != nil {
		return nil, nil, err
	}
	hasSig := false
	for n, node := range t {
		if strings.HasPrefix(n, prefix+"signatures/") && node.Kind == "f" {
			hasSig = true
			break
		}
	}
	if !hasSig {
		return nil, nil, fmt.Errorf("No signature files (presence is not cryptographic verification)")
	}
	return info, carrier, nil
}

func StagingArchive(payload Tree) ([]byte, error) {
	t := Tree{
		"META-INF":     {Kind: "d"},
		"p0":           {Kind: "d"},
		"p0/p1":        {Kind: "d"},
		"p0/p1/p2":     {Kind: "d"},
		"p0/p1/p2/link": {Kind: "l", Data: []byte("../../../" + Parent[1:])},
	}
	meta, err := plist.Marshal(map[string]interface{}{"Version": 2}, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	t["META-INF/com.apple.ZipMetadata.plist"] = Node{Kind: "f", Data: meta}

	mkdirs := func(p string) {
		cursor := ""
		for _, part := range strings.Split(p, "/") {
			if cursor == "" {
				cursor = part
			} else {
				cursor += "/" + part
			}
			t[cursor] = Node{Kind: "d"}
		}
	}
	mkdirs(Parent[1:])
	if payload != nil {
		mkdirs(PayloadPath)
		systemNames := map[string]struct{}{}
		reBundle := regexp.MustCompile(`^[A-Za-z0-9_]+\.bundle$`)
		for _, node := range payload {
			if node.Kind == "l" && strings.HasPrefix(string(node.Data), SystemPrefix) {
				name := strings.TrimPrefix(string(node.Data), SystemPrefix)
				if !reBundle.MatchString(name) {
					return nil, fmt.Errorf("Неожиданная системная ссылка")
				}
				systemNames[name] = struct{}{}
			}
		}
		for name := range systemNames {
			mkdirs("System/Library/Carrier Bundles/iPhone/" + name)
		}
		for n, v := range payload {
			t[PayloadPath+"/"+n] = v
		}
	}
	return WriteZipBytes(t, true)
}
