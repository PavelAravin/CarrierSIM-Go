package trigger

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"carriersim/internal/afcops"
	"carriersim/internal/plan"
	"carriersim/internal/run"
	"carriersim/internal/tree"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/syslog"
)

var (
	reSupportedSIM = regexp.MustCompile(`^\d{5,6}(?:_.*)?$`)
	reLeafSIM      = regexp.MustCompile(`^\d{5,6}(?:_.*)?$`)
)

type CheckResult struct {
	Bundle  string
	Version interface{}
	SHA256  string
}

func CheckTrigger(path string, sims map[string]struct{}) (CheckResult, error) {
	if filepath.Ext(path) != ".ipcc" {
		return CheckResult{}, fmt.Errorf("Trigger must be an IPCC")
	}
	t, err := tree.ReadZip(path)
	if err != nil {
		return CheckResult{}, err
	}
	bundles := map[string]struct{}{}
	for n := range t {
		if strings.HasPrefix(n, "Payload/") {
			parts := strings.Split(n, "/")
			if len(parts) > 1 && strings.HasSuffix(parts[1], ".bundle") {
				bundles[parts[1]] = struct{}{}
			}
		}
	}
	if len(bundles) != 1 {
		return CheckResult{}, fmt.Errorf("Trigger must contain exactly one bundle")
	}
	var name string
	for b := range bundles {
		name = b
	}
	inner := tree.Tree{}
	for n, v := range t {
		if strings.HasPrefix(n, "Payload/") {
			inner[strings.TrimPrefix(n, "Payload/")] = v
		}
	}
	info, carrier, err := tree.BundleInfo(inner, name)
	if err != nil {
		return CheckResult{}, err
	}
	if name == tree.Bundle {
		return CheckResult{}, fmt.Errorf("Viva is not an independent trigger")
	}
	if id, _ := info["CFBundleIdentifier"].(string); id == "com.apple.Viva_kw" {
		return CheckResult{}, fmt.Errorf("Viva is not an independent trigger")
	}
	identifiers, _ := carrier["SupportedSIMs"].([]interface{})
	if len(identifiers) == 0 {
		return CheckResult{}, fmt.Errorf("Unknown SupportedSIMs format in trigger")
	}
	affected := map[string]struct{}{}
	for _, s := range identifiers {
		str, ok := s.(string)
		if !ok || !reSupportedSIM.MatchString(str) {
			return CheckResult{}, fmt.Errorf("Unknown SupportedSIMs format in trigger")
		}
		affected[str] = struct{}{}
	}
	for n, node := range t {
		if node.Kind == "l" {
			leaf := n
			if i := strings.LastIndex(n, "/"); i >= 0 {
				leaf = n[i+1:]
			}
			if !reLeafSIM.MatchString(leaf) {
				return CheckResult{}, fmt.Errorf("Unexpected trigger symlink")
			}
			affected[leaf] = struct{}{}
		}
	}
	for a := range affected {
		for s := range sims {
			if a == s || strings.HasPrefix(a, s+"_") {
				return CheckResult{}, fmt.Errorf("Trigger overlaps an installed SIM; select a different carrier")
			}
		}
	}
	raw, _ := os.ReadFile(path)
	return CheckResult{
		Bundle:  name,
		Version: info["CFBundleVersion"],
		SHA256:  tree.Digest(raw),
	}, nil
}

func CheckHardware(path, hardware string) bool {
	t, err := tree.ReadZip(path)
	if err != nil {
		return false
	}
	board := strings.ToUpper(hardware)
	board = strings.TrimSuffix(board, "AP")
	for name, node := range t {
		if node.Kind != "f" {
			continue
		}
		if strings.Contains(name, "/signatures/") {
			continue
		}
		leaf := name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			leaf = name[i+1:]
		}
		if !strings.HasPrefix(leaf, "overrides_") || !strings.HasSuffix(leaf, ".plist") {
			continue
		}
		boards := strings.Split(strings.TrimSuffix(strings.TrimPrefix(leaf, "overrides_"), ".plist"), "_")
		for i := range boards {
			boards[i] = strings.ToUpper(boards[i])
		}
		match := false
		for _, b := range boards {
			if b == board {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		sig := name[:strings.LastIndex(name, "/")+1] + "signatures/" + leaf
		if _, ok := t[sig]; ok {
			return true
		}
		break
	}
	fmt.Println("Предупреждение: в IPCC нет настроек с подписью для платы " + hardware +
		". Пересканирование МОЖЕТ не работать; продолжаю.")
	return false
}

func Install(entry ios.DeviceEntry, ipccPath, runDir string) (map[string]interface{}, error) {
	status := map[string]interface{}{
		"ipcc_installation_completed": false,
		"log_error":                   nil,
		"nr_data_verified":            false,
	}
	logPath := filepath.Join(runDir, "commcenter.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return status, err
	}
	defer logFile.Close()

	stop := make(chan struct{})
	ready := make(chan struct{})
	go func() {
		sl, err := syslog.New(entry)
		if err != nil {
			status["log_error"] = err.Error()
			close(ready)
			return
		}
		defer sl.Close()
		close(ready)
		size := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			line, err := sl.ReadLogMessage()
			if err != nil {
				status["log_error"] = err.Error()
				return
			}
			if strings.Contains(line, "CommCenter") {
				size += len(line)
				if size >= 16*1024*1024 {
					status["log_error"] = "Log limit reached"
					return
				}
				_, _ = logFile.WriteString(line + "\n")
				_ = logFile.Sync()
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		close(stop)
		return status, fmt.Errorf("syslog not ready")
	}

	if err := installIPCC(entry, ipccPath); err != nil {
		status["installation_error"] = err.Error()
		close(stop)
		_ = run.SaveJSON(filepath.Join(runDir, "installation.json"), status)
		return status, err
	}
	status["ipcc_installation_completed"] = true
	_ = run.SaveJSON(filepath.Join(runDir, "installation.json"), status)
	time.Sleep(8 * time.Second)
	close(stop)
	_ = run.SaveJSON(filepath.Join(runDir, "installation.json"), status)
	return status, nil
}

func installIPCC(entry ios.DeviceEntry, ipccPath string) error {
	c, err := afcops.Open(entry)
	if err != nil {
		return err
	}
	defer c.Close()

	dst := "PublicStaging/CarrierSIM.ipcc"
	_ = c.Remove(dst)
	_ = c.MkDirs(dst)

	zr, err := zip.OpenReader(ipccPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target := dst + "/" + f.Name
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := c.MkDirs(strings.TrimSuffix(target, "/")); err != nil {
				return err
			}
			continue
		}
		if err := c.MkDirs(filepath.ToSlash(filepath.Dir(target))); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data := make([]byte, f.UncompressedSize64)
		_, err = rc.Read(data)
		rc.Close()
		if err != nil && err.Error() != "EOF" {
			// ReadAll-style
			rc2, e2 := f.Open()
			if e2 != nil {
				return e2
			}
			buf := make([]byte, 0, f.UncompressedSize64)
			tmp := make([]byte, 32*1024)
			for {
				n, e := rc2.Read(tmp)
				if n > 0 {
					buf = append(buf, tmp[:n]...)
				}
				if e != nil {
					break
				}
			}
			rc2.Close()
			data = buf
		}
		if err := c.SetFile(target, data); err != nil {
			return err
		}
	}

	conn, err := ios.ConnectToService(entry, "com.apple.mobile.installation_proxy")
	if err != nil {
		return err
	}
	defer conn.Close()
	codec := ios.NewPlistCodecReadWriter(conn.Reader(), conn.Writer())
	cmd := map[string]interface{}{
		"Command":       "Install",
		"PackagePath":   dst,
		"ClientOptions": map[string]interface{}{"PackageType": "CarrierBundle"},
	}
	if err := codec.Write(cmd); err != nil {
		return err
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var reply map[string]interface{}
		if err := codec.Read(&reply); err != nil {
			return err
		}
		if status, _ := reply["Status"].(string); status == "Complete" {
			return nil
		}
		if errStr, ok := reply["Error"].(string); ok && errStr != "" {
			return fmt.Errorf("installation_proxy: %s", errStr)
		}
	}
	return fmt.Errorf("IPCC install timeout")
}

func ReportLog(path string, sims []plan.SIM) []map[string]interface{} {
	results := map[string]map[string]interface{}{}
	for _, s := range sims {
		results[s.Slot] = map[string]interface{}{
			"slot": s.Slot, "plmn": s.PLMN, "expected": s.Bundle,
			"selected": nil, "verified": false,
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		out := make([]map[string]interface{}, 0, len(results))
		for _, s := range sims {
			out = append(out, results[s.Slot])
		}
		return out
	}
	blocks := strings.Split(string(raw), "----------Bundle File----------")
	reResolved := regexp.MustCompile(`Resolved path\s*:\s*([^\r\n]+)`)
	reLinked := regexp.MustCompile(`Linking Path\s*:\s*([^\r\n]+)`)
	reVerified := regexp.MustCompile(`Verification Result\s*:\s*([^\r\n]+)`)
	for _, block := range blocks {
		resolved := reResolved.FindStringSubmatch(block)
		linked := reLinked.FindStringSubmatch(block)
		verified := reVerified.FindStringSubmatch(block)
		if len(resolved) != 2 || len(linked) != 2 {
			continue
		}
		for slot, index := range map[string]int{"kOne": 1, "kTwo": 2} {
			if _, ok := results[slot]; !ok {
				continue
			}
			if strings.HasSuffix(strings.TrimSpace(linked[1]), fmt.Sprintf("/Carrier%dBundle.bundle", index)) {
				sel := strings.TrimSpace(resolved[1])
				if i := strings.LastIndex(sel, "/"); i >= 0 {
					sel = sel[i+1:]
				}
				results[slot]["selected"] = sel
				results[slot]["verified"] = len(verified) == 2 && verified[1] == "Success"
			}
		}
	}
	out := make([]map[string]interface{}, 0, len(sims))
	for _, s := range sims {
		out = append(out, results[s.Slot])
	}
	return out
}
