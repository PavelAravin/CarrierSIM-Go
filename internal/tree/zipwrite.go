package tree

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"time"
)

// WriteZipBytes builds a STORE-only ZIP without data descriptors, matching
// pymobiledevice3/CarrierSIM staging for streaming_zip_conduit.
func WriteZipBytes(t Tree, streaming bool) ([]byte, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	type central struct {
		offset uint32
		hdr    []byte
	}
	var centrals []central
	modTime := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	dostime, dosdate := timeToMsDos(modTime)

	for _, name := range names {
		n := t[name]
		mode := unixMode(n.Kind)
		entryName := name
		if n.Kind == "d" && !bytes.HasSuffix([]byte(entryName), []byte("/")) {
			entryName += "/"
		}
		nameBytes := []byte(entryName)
		var extra []byte
		if streaming {
			extra = make([]byte, 6)
			binary.LittleEndian.PutUint16(extra[0:], 0x5A53)
			binary.LittleEndian.PutUint16(extra[2:], 2)
			binary.LittleEndian.PutUint16(extra[4:], uint16(mode))
		}
		crc := crc32.ChecksumIEEE(n.Data)
		size := uint32(len(n.Data))
		offset := uint32(buf.Len())

		// Local file header
		lh := make([]byte, 30)
		binary.LittleEndian.PutUint32(lh[0:], 0x04034b50)
		binary.LittleEndian.PutUint16(lh[4:], 20) // version needed
		binary.LittleEndian.PutUint16(lh[6:], 0)  // flags — no data descriptor
		binary.LittleEndian.PutUint16(lh[8:], 0)  // STORE
		binary.LittleEndian.PutUint16(lh[10:], dostime)
		binary.LittleEndian.PutUint16(lh[12:], dosdate)
		binary.LittleEndian.PutUint32(lh[14:], crc)
		binary.LittleEndian.PutUint32(lh[18:], size)
		binary.LittleEndian.PutUint32(lh[22:], size)
		binary.LittleEndian.PutUint16(lh[26:], uint16(len(nameBytes)))
		binary.LittleEndian.PutUint16(lh[28:], uint16(len(extra)))
		buf.Write(lh)
		buf.Write(nameBytes)
		buf.Write(extra)
		buf.Write(n.Data)

		// Central directory header
		ch := make([]byte, 46)
		binary.LittleEndian.PutUint32(ch[0:], 0x02014b50)
		binary.LittleEndian.PutUint16(ch[4:], (3<<8)|20) // create system Unix | version
		binary.LittleEndian.PutUint16(ch[6:], 20)        // version needed
		binary.LittleEndian.PutUint16(ch[8:], 0)         // flags
		binary.LittleEndian.PutUint16(ch[10:], 0)        // STORE
		binary.LittleEndian.PutUint16(ch[12:], dostime)
		binary.LittleEndian.PutUint16(ch[14:], dosdate)
		binary.LittleEndian.PutUint32(ch[16:], crc)
		binary.LittleEndian.PutUint32(ch[20:], size)
		binary.LittleEndian.PutUint32(ch[24:], size)
		binary.LittleEndian.PutUint16(ch[28:], uint16(len(nameBytes)))
		binary.LittleEndian.PutUint16(ch[30:], uint16(len(extra)))
		binary.LittleEndian.PutUint16(ch[32:], 0) // comment
		binary.LittleEndian.PutUint16(ch[34:], 0) // disk
		binary.LittleEndian.PutUint16(ch[36:], 0) // internal attrs
		binary.LittleEndian.PutUint32(ch[38:], mode<<16)
		binary.LittleEndian.PutUint32(ch[42:], offset)
		var cbuf bytes.Buffer
		cbuf.Write(ch)
		cbuf.Write(nameBytes)
		cbuf.Write(extra)
		centrals = append(centrals, central{offset: offset, hdr: cbuf.Bytes()})
	}

	cdOffset := uint32(buf.Len())
	cdSize := uint32(0)
	for _, c := range centrals {
		buf.Write(c.hdr)
		cdSize += uint32(len(c.hdr))
	}
	// End of central directory
	eo := make([]byte, 22)
	binary.LittleEndian.PutUint32(eo[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(eo[4:], 0)
	binary.LittleEndian.PutUint16(eo[6:], 0)
	binary.LittleEndian.PutUint16(eo[8:], uint16(len(centrals)))
	binary.LittleEndian.PutUint16(eo[10:], uint16(len(centrals)))
	binary.LittleEndian.PutUint32(eo[12:], cdSize)
	binary.LittleEndian.PutUint32(eo[16:], cdOffset)
	binary.LittleEndian.PutUint16(eo[20:], 0)
	buf.Write(eo)
	return buf.Bytes(), nil
}

func unixMode(kind string) uint32 {
	switch kind {
	case "d":
		return 0o040755
	case "l":
		return 0o120777
	default:
		return 0o100644
	}
}

func timeToMsDos(t time.Time) (uint16, uint16) {
	t = t.UTC()
	timeVal := uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	dateVal := uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	return timeVal, dateVal
}

// Keep WriteZip using archive/zip for local backups (seekable file is fine).
func writeZipTo(path string, t Tree) error {
	raw, err := WriteZipBytes(t, false)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

var _ = io.EOF
