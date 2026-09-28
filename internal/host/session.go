package host

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"carriersim/internal/run"
)

type Asset struct {
	ID   string
	Dest string
}

// Session runs --_host in a subprocess and calls pause before the final asset.
func Session(exe, udid string, assets []Asset, directories []string, runDir string, pause func() error) error {
	config := filepath.Join(runDir, "host-input.json")
	pairs := make([][]string, len(assets))
	for i, a := range assets {
		pairs[i] = []string{a.ID, a.Dest}
	}
	if err := run.SaveJSON(config, map[string]interface{}{
		"udid":        udid,
		"assets":      pairs,
		"directories": directories,
	}); err != nil {
		return err
	}
	defer os.Remove(config)

	cmd := exec.Command(exe, "--_host", config)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	errFile, _ := os.Create(filepath.Join(runDir, "host.stderr"))
	go func() {
		defer errFile.Close()
		buf := make([]byte, 4096)
		for {
			n, e := stderr.Read(buf)
			if n > 0 {
				_, _ = errFile.Write(buf[:n])
			}
			if e != nil {
				return
			}
		}
	}()

	logFile, err := os.Create(filepath.Join(runDir, "host.jsonl"))
	if err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	defer logFile.Close()

	paused := false
	var result map[string]interface{}
	done := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Bytes()
			_, _ = logFile.Write(append(line, '\n'))
			_ = logFile.Sync()
			s := string(line)
			if !strings.HasPrefix(s, "CARRIER_SWAP_JSON:") {
				continue
			}
			var row map[string]interface{}
			if err := json.Unmarshal([]byte(s[len("CARRIER_SWAP_JSON:"):]), &row); err != nil {
				done <- err
				return
			}
			if ev, _ := row["event"].(string); ev == "before-final-asset" {
				if paused {
					done <- fmt.Errorf("Повторная пауза AirTraffic")
					return
				}
				if err := pause(); err != nil {
					done <- err
					return
				}
				paused = true
				_, _ = stdin.Write([]byte("CONTINUE\n"))
			} else {
				result = row
			}
		}
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return err
		}
	case <-time.After(170 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("Сбой AirTraffic; сохраните каталог операции для --recover")
	}
	if !paused || result == nil {
		return fmt.Errorf("Сбой AirTraffic; сохраните каталог операции для --recover")
	}
	if ok, _ := result["ok"].(bool); !ok {
		return fmt.Errorf("Сбой AirTraffic; сохраните каталог операции для --recover")
	}
	return nil
}

func Framed(v interface{}) {
	raw, _ := json.Marshal(v)
	fmt.Println("CARRIER_SWAP_JSON:" + string(raw))
}

// CheckLibraries loads Apple libs without connecting to a device.
func CheckLibraries(directories []string) error {
	return NativeHost("", nil, directories)
}
