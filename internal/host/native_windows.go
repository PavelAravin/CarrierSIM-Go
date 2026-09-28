//go:build windows

package host

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"howett.net/plist"
)

func randRead(b []byte) (int, error) {
	return rand.Read(b)
}

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	procLoadLib = modKernel32.NewProc("LoadLibraryExW")
	procGetProc = modKernel32.NewProc("GetProcAddress")
	procFreeLib = modKernel32.NewProc("FreeLibrary")
	procSetDll  = modKernel32.NewProc("SetDllDirectoryW")
)

const loadLibrarySearchDefaultDirs = 0x1000
const loadLibrarySearchDLLLoadDir = 0x100

type appleHost struct {
	cf, at                           syscall.Handle
	cfDataCreate                     uintptr
	cfDataGetLength                  uintptr
	cfDataGetBytePtr                 uintptr
	cfRelease                        uintptr
	cfPropertyListCreateWithData     uintptr
	cfPropertyListCreateData         uintptr
	atHostConnectionCreate           uintptr
	atHostConnectionRelease          uintptr
	atHostConnectionReadMessage      uintptr
	atHostConnectionSendHostInfo     uintptr
	atHostConnectionSendSyncRequest  uintptr
	atHostConnectionSendMetadataSync uintptr
	atHostConnectionSendAssetDone    uintptr
	atCFMessageGetName               uintptr
	atCFMessageGetParam              uintptr
}

func loadDLL(name string, dirs []string) (syscall.Handle, error) {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		pathPtr, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			return 0, err
		}
		r, _, e := procLoadLib.Call(uintptr(unsafe.Pointer(pathPtr)), 0, loadLibrarySearchDefaultDirs|loadLibrarySearchDLLLoadDir)
		if r != 0 {
			return syscall.Handle(r), nil
		}
		_ = e
	}
	return 0, fmt.Errorf("Не найдена %s. Установите iTunes x64 с сайта Apple или укажите --apple-dir.", name)
}

func getProc(h syscall.Handle, name string) (uintptr, error) {
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	r, _, e := procGetProc.Call(uintptr(h), uintptr(unsafe.Pointer(n)))
	if r == 0 {
		return 0, fmt.Errorf("GetProcAddress %s: %v", name, e)
	}
	return r, nil
}

func newAppleHost(directories []string) (*appleHost, error) {
	paths := make([]string, 0, len(directories)+4)
	for _, p := range directories {
		if abs, err := filepath.Abs(p); err == nil {
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				paths = append(paths, abs)
			}
		}
	}
	for _, key := range []string{"CommonProgramW6432", "CommonProgramFiles"} {
		base := os.Getenv(key)
		if base == "" {
			continue
		}
		paths = append(paths,
			filepath.Join(base, "Apple", "Mobile Device Support"),
			filepath.Join(base, "Apple", "Apple Application Support"),
		)
	}
	seen := map[string]bool{}
	uniq := paths[:0]
	for _, p := range paths {
		if !seen[p] {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				seen[p] = true
				uniq = append(uniq, p)
			}
		}
	}
	paths = uniq
	for _, p := range paths {
		ptr, _ := syscall.UTF16PtrFromString(p)
		procSetDll.Call(uintptr(unsafe.Pointer(ptr)))
	}
	cf, err := loadDLL("CoreFoundation.dll", paths)
	if err != nil {
		return nil, err
	}
	at, err := loadDLL("AirTrafficHost.dll", paths)
	if err != nil {
		procFreeLib.Call(uintptr(cf))
		return nil, err
	}
	h := &appleHost{cf: cf, at: at}
	must := func(lib syscall.Handle, name string) uintptr {
		p, e := getProc(lib, name)
		if e != nil {
			panic(e)
		}
		return p
	}
	defer func() {
		if r := recover(); r != nil {
			procFreeLib.Call(uintptr(cf))
			procFreeLib.Call(uintptr(at))
			err = fmt.Errorf("%v", r)
		}
	}()
	h.cfDataCreate = must(cf, "CFDataCreate")
	h.cfDataGetLength = must(cf, "CFDataGetLength")
	h.cfDataGetBytePtr = must(cf, "CFDataGetBytePtr")
	h.cfRelease = must(cf, "CFRelease")
	h.cfPropertyListCreateWithData = must(cf, "CFPropertyListCreateWithData")
	h.cfPropertyListCreateData = must(cf, "CFPropertyListCreateData")
	h.atHostConnectionCreate = must(at, "ATHostConnectionCreate")
	h.atHostConnectionRelease = must(at, "ATHostConnectionRelease")
	h.atHostConnectionReadMessage = must(at, "ATHostConnectionReadMessage")
	h.atHostConnectionSendHostInfo = must(at, "ATHostConnectionSendHostInfo")
	h.atHostConnectionSendSyncRequest = must(at, "ATHostConnectionSendSyncRequest")
	h.atHostConnectionSendMetadataSync = must(at, "ATHostConnectionSendMetadataSyncFinished")
	h.atHostConnectionSendAssetDone = must(at, "ATHostConnectionSendAssetCompleted")
	h.atCFMessageGetName = must(at, "ATCFMessageGetName")
	h.atCFMessageGetParam = must(at, "ATCFMessageGetParam")
	return h, err
}

func (h *appleHost) close() {
	if h.at != 0 {
		procFreeLib.Call(uintptr(h.at))
	}
	if h.cf != 0 {
		procFreeLib.Call(uintptr(h.cf))
	}
}

func callFn(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

func (h *appleHost) release(ref uintptr) {
	if ref != 0 {
		callFn(h.cfRelease, ref)
	}
}

func (h *appleHost) encode(value interface{}) (uintptr, error) {
	raw, err := plist.Marshal(value, plist.BinaryFormat)
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty plist")
	}
	data := callFn(h.cfDataCreate, 0, uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)))
	if data == 0 {
		return 0, fmt.Errorf("CFDataCreate failed")
	}
	defer h.release(data)
	result := callFn(h.cfPropertyListCreateWithData, 0, data, 0, 0, 0)
	if result == 0 {
		return 0, fmt.Errorf("CFPropertyListCreateWithData failed")
	}
	return result, nil
}

func (h *appleHost) decode(value uintptr) (interface{}, error) {
	if value == 0 {
		return nil, fmt.Errorf("Пустое сообщение Apple")
	}
	data := callFn(h.cfPropertyListCreateData, 0, value, 200, 0, 0)
	if data == 0 {
		return nil, fmt.Errorf("CFPropertyListCreateData failed")
	}
	defer h.release(data)
	size := int(callFn(h.cfDataGetLength, data))
	if size < 0 || size > 64*1024*1024 {
		return nil, fmt.Errorf("Слишком большое сообщение Apple")
	}
	ptr := callFn(h.cfDataGetBytePtr, data)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
	cp := make([]byte, size)
	copy(cp, buf)
	var out interface{}
	if _, err := plist.Unmarshal(cp, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *appleHost) call(fn uintptr, connection uintptr, values ...interface{}) error {
	refs := make([]uintptr, len(values))
	defer func() {
		for _, r := range refs {
			h.release(r)
		}
	}()
	for i, v := range values {
		ref, err := h.encode(v)
		if err != nil {
			return err
		}
		refs[i] = ref
	}
	args := make([]uintptr, 0, 1+len(refs))
	args = append(args, connection)
	args = append(args, refs...)
	callFn(fn, args...)
	return nil
}

// NativeHost implements AirTraffic sync (Windows).
func NativeHost(udid string, assets [][]string, directories []string) error {
	h, err := newAppleHost(directories)
	if err != nil {
		return err
	}
	defer h.close()

	sample := map[string]interface{}{"test": []interface{}{"Book", 1, false}}
	ref, err := h.encode(sample)
	if err != nil {
		return err
	}
	decoded, err := h.decode(ref)
	h.release(ref)
	if err != nil {
		return err
	}
	// Round-trip check loosely
	_ = decoded
	if udid == "" {
		Framed(map[string]interface{}{"ok": true, "deviceConnections": 0})
		return nil
	}

	udidRef, err := h.encode(udid)
	if err != nil {
		return err
	}
	connection := callFn(h.atHostConnectionCreate, udidRef)
	h.release(udidRef)
	if connection == 0 {
		return fmt.Errorf("Не удалось открыть AirTraffic. Закройте синхронизацию iTunes/Finder.")
	}
	defer callFn(h.atHostConnectionRelease, connection)

	until := func(wanted string, limit int) (interface{}, error) {
		for i := 0; i < limit; i++ {
			msg := callFn(h.atHostConnectionReadMessage, connection)
			if msg == 0 {
				continue
			}
			nameRef := callFn(h.atCFMessageGetName, msg)
			nameVal, err := h.decode(nameRef)
			if err != nil {
				h.release(msg)
				return nil, err
			}
			name, _ := nameVal.(string)
			if name == wanted {
				if name != "AssetManifest" {
					h.release(msg)
					return true, nil
				}
				key, err := h.encode("AssetManifest")
				if err != nil {
					h.release(msg)
					return nil, err
				}
				param := callFn(h.atCFMessageGetParam, msg, key)
				h.release(key)
				manifest, err := h.decode(param)
				h.release(msg)
				return manifest, err
			}
			if name == "SyncFailed" || name == "SyncFinished" {
				h.release(msg)
				return nil, fmt.Errorf("Синхронизация закончилась преждевременно")
			}
			h.release(msg)
		}
		return nil, fmt.Errorf("Не получено сообщение %s", wanted)
	}

	if _, err := until("SyncAllowed", 8); err != nil {
		return err
	}
	info := map[string]interface{}{
		"Type":               "iTunes",
		"Version":            "13.7.0.161",
		"SyncHostName":       "CarrierSIM",
		"LibraryID":          newUUID(),
		"SyncedDataclasses":  []string{"Book"},
		"SyncedAssetTypes":   []string{"Book"},
		"Wakeable":           false,
	}
	if err := h.call(h.atHostConnectionSendHostInfo, connection, info); err != nil {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	if err := h.call(h.atHostConnectionSendSyncRequest, connection, []string{"Book"}, map[string]interface{}{}, info); err != nil {
		return err
	}
	if _, err := until("ReadyForSync", 12); err != nil {
		return err
	}
	if err := h.call(h.atHostConnectionSendMetadataSync, connection, map[string]interface{}{"Book": 1}, map[string]interface{}{}); err != nil {
		return err
	}
	manifestRaw, err := until("AssetManifest", 20)
	if err != nil {
		return err
	}
	manifest, ok := manifestRaw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("Неверный манифест AirTraffic")
	}
	found := map[string]bool{}
	if books, ok := manifest["Book"].([]interface{}); ok {
		for _, row := range books {
			m, ok := row.(map[string]interface{})
			if !ok {
				continue
			}
			if id, _ := m["AssetID"].(string); id != "" {
				if dl, _ := m["IsDownload"].(bool); dl {
					found[id] = true
				}
			}
		}
	}
	for _, a := range assets {
		if len(a) < 1 || !found[a[0]] {
			return fmt.Errorf("AirTraffic не подтвердил нужные объекты")
		}
	}
	for i, a := range assets {
		if i == 2 {
			Framed(map[string]interface{}{"event": "before-final-asset"})
			var line string
			if _, err := fmt.Scanln(&line); err != nil || line != "CONTINUE" {
				return fmt.Errorf("Резервная копия не подтверждена")
			}
		}
		if err := h.call(h.atHostConnectionSendAssetDone, connection, a[0], "Book", a[1]); err != nil {
			return err
		}
		if i+1 < len(assets) {
			time.Sleep(900 * time.Millisecond)
		}
	}
	time.Sleep(2 * time.Second)
	Framed(map[string]interface{}{"ok": true})
	return nil
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := randRead(b); err != nil {
		raw, _ := json.Marshal(time.Now().UnixNano())
		copy(b, raw)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func RunHostFromConfig(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg struct {
		UDID        string     `json:"udid"`
		Assets      [][]string `json:"assets"`
		Directories []string   `json:"directories"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	return NativeHost(cfg.UDID, cfg.Assets, cfg.Directories)
}
