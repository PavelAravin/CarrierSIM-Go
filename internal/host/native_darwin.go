//go:build darwin

package host

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"unsafe"

	"howett.net/plist"
)

/*
#cgo LDFLAGS: -framework CoreFoundation -lobjc
#include <stdlib.h>
#include <objc/objc.h>
#include <objc/runtime.h>
#include <objc/message.h>

typedef const void* CFTypeRef;
typedef const struct __CFData* CFDataRef;
typedef const struct __CFAllocator* CFAllocatorRef;
typedef unsigned long CFIndex;
typedef unsigned long CFOptionFlags;
typedef CFIndex CFPropertyListFormat;

extern CFDataRef CFDataCreate(CFAllocatorRef, const UInt8*, CFIndex);
extern CFIndex CFDataGetLength(CFDataRef);
extern const UInt8* CFDataGetBytePtr(CFDataRef);
extern void CFRelease(CFTypeRef);
extern CFTypeRef CFPropertyListCreateWithData(CFAllocatorRef, CFDataRef, CFOptionFlags, CFPropertyListFormat*, void*);
extern CFDataRef CFPropertyListCreateData(CFAllocatorRef, CFTypeRef, CFPropertyListFormat, CFOptionFlags, void*);

void* ATHostConnectionCreate(CFTypeRef);
void ATHostConnectionRelease(void*);
CFTypeRef ATHostConnectionReadMessage(void*);
void ATHostConnectionSendHostInfo(void*, CFTypeRef);
void ATHostConnectionSendSyncRequest(void*, CFTypeRef, CFTypeRef, CFTypeRef);
void ATHostConnectionSendMetadataSyncFinished(void*, CFTypeRef, CFTypeRef);
void ATHostConnectionSendAssetCompleted(void*, CFTypeRef, CFTypeRef, CFTypeRef);
CFTypeRef ATCFMessageGetName(CFTypeRef);
CFTypeRef ATCFMessageGetParam(CFTypeRef, CFTypeRef);

void* objc_autoreleasePoolPush(void);
void objc_autoreleasePoolPop(void*);
*/
import "C"

type appleHost struct {
	pool unsafe.Pointer
}

func newAppleHost(_ []string) (*appleHost, error) {
	return &appleHost{pool: C.objc_autoreleasePoolPush()}, nil
}

func (h *appleHost) close() {
	if h.pool != nil {
		C.objc_autoreleasePoolPop(h.pool)
		h.pool = nil
	}
}

func (h *appleHost) encode(value interface{}) (C.CFTypeRef, error) {
	raw, err := plist.Marshal(value, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	data := C.CFDataCreate(nil, (*C.UInt8)(unsafe.Pointer(&raw[0])), C.CFIndex(len(raw)))
	if data == nil {
		return nil, fmt.Errorf("CFDataCreate failed")
	}
	defer C.CFRelease(C.CFTypeRef(data))
	result := C.CFPropertyListCreateWithData(nil, data, 0, nil, nil)
	if result == nil {
		return nil, fmt.Errorf("CFPropertyListCreateWithData failed")
	}
	return result, nil
}

func (h *appleHost) decode(value C.CFTypeRef) (interface{}, error) {
	if value == nil {
		return nil, fmt.Errorf("Пустое сообщение Apple")
	}
	data := C.CFPropertyListCreateData(nil, value, 200, 0, nil)
	if data == nil {
		return nil, fmt.Errorf("CFPropertyListCreateData failed")
	}
	defer C.CFRelease(C.CFTypeRef(data))
	size := int(C.CFDataGetLength(data))
	if size < 0 || size > 64*1024*1024 {
		return nil, fmt.Errorf("Слишком большое сообщение Apple")
	}
	ptr := C.CFDataGetBytePtr(data)
	buf := C.GoBytes(unsafe.Pointer(ptr), C.int(size))
	var out interface{}
	if _, err := plist.Unmarshal(buf, &out); err != nil {
		return nil, err
	}
	return out, nil
}

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
	_, err = h.decode(ref)
	C.CFRelease(ref)
	if err != nil {
		return err
	}
	if udid == "" {
		Framed(map[string]interface{}{"ok": true, "deviceConnections": 0})
		return nil
	}

	udidRef, err := h.encode(udid)
	if err != nil {
		return err
	}
	connection := C.ATHostConnectionCreate(udidRef)
	C.CFRelease(udidRef)
	if connection == nil {
		return fmt.Errorf("Не удалось открыть AirTraffic. Закройте синхронизацию iTunes/Finder.")
	}
	defer C.ATHostConnectionRelease(connection)

	until := func(wanted string, limit int) (interface{}, error) {
		for i := 0; i < limit; i++ {
			msg := C.ATHostConnectionReadMessage(connection)
			if msg == nil {
				continue
			}
			nameVal, err := h.decode(C.ATCFMessageGetName(msg))
			if err != nil {
				C.CFRelease(msg)
				return nil, err
			}
			name, _ := nameVal.(string)
			if name == wanted {
				if name != "AssetManifest" {
					C.CFRelease(msg)
					return true, nil
				}
				key, err := h.encode("AssetManifest")
				if err != nil {
					C.CFRelease(msg)
					return nil, err
				}
				param := C.ATCFMessageGetParam(msg, key)
				C.CFRelease(key)
				manifest, err := h.decode(param)
				C.CFRelease(msg)
				return manifest, err
			}
			if name == "SyncFailed" || name == "SyncFinished" {
				C.CFRelease(msg)
				return nil, fmt.Errorf("Синхронизация закончилась преждевременно")
			}
			C.CFRelease(msg)
		}
		return nil, fmt.Errorf("Не получено сообщение %s", wanted)
	}

	if _, err := until("SyncAllowed", 8); err != nil {
		return err
	}
	info := map[string]interface{}{
		"Type": "iTunes", "Version": "13.7.0.161", "SyncHostName": "CarrierSIM",
		"LibraryID": newUUID(), "SyncedDataclasses": []string{"Book"},
		"SyncedAssetTypes": []string{"Book"}, "Wakeable": false,
	}
	infoRef, _ := h.encode(info)
	C.ATHostConnectionSendHostInfo(connection, infoRef)
	C.CFRelease(infoRef)
	time.Sleep(200 * time.Millisecond)
	booksRef, _ := h.encode([]string{"Book"})
	emptyRef, _ := h.encode(map[string]interface{}{})
	infoRef2, _ := h.encode(info)
	C.ATHostConnectionSendSyncRequest(connection, booksRef, emptyRef, infoRef2)
	C.CFRelease(booksRef)
	C.CFRelease(emptyRef)
	C.CFRelease(infoRef2)
	if _, err := until("ReadyForSync", 12); err != nil {
		return err
	}
	metaRef, _ := h.encode(map[string]interface{}{"Book": 1})
	empty2, _ := h.encode(map[string]interface{}{})
	C.ATHostConnectionSendMetadataSyncFinished(connection, metaRef, empty2)
	C.CFRelease(metaRef)
	C.CFRelease(empty2)
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
			m, _ := row.(map[string]interface{})
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
			fmt.Scanln(&line)
			if line != "CONTINUE" {
				return fmt.Errorf("Резервная копия не подтверждена")
			}
		}
		idRef, _ := h.encode(a[0])
		typeRef, _ := h.encode("Book")
		destRef, _ := h.encode(a[1])
		C.ATHostConnectionSendAssetCompleted(connection, idRef, typeRef, destRef)
		C.CFRelease(idRef)
		C.CFRelease(typeRef)
		C.CFRelease(destRef)
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
	f, _ := os.Open("/dev/urandom")
	if f != nil {
		_, _ = f.Read(b)
		f.Close()
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
