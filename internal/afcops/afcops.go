package afcops

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"carriersim/internal/run"
	"carriersim/internal/tree"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/afc"
	"howett.net/plist"
)

var (
	BookFiles = []string{
		"Books/Books.plist",
		"Books/Sync/Books.plist",
		"Books/Sync/Upload.plist",
		"Books/Sync/Database/OutstandingAssets_4.sqlite",
		"Books/Sync/Database/OutstandingAssets_4.sqlite-shm",
		"Books/Sync/Database/OutstandingAssets_4.sqlite-wal",
	}
	BookDirs = []string{"Books", "Books/Sync", "Books/Sync/Database"}
	// Locks AirTraffic may create; keep if they already existed.
	BookLocks = []string{"Managed/.Managed.plist.lock", "Sync/.bookSync.lock"}
)

type FileInfo struct {
	Ifmt       string
	Size       int64
	LinkTarget string
}

func (f FileInfo) IsDir() bool  { return f.Ifmt == "S_IFDIR" }
func (f FileInfo) IsLink() bool { return f.Ifmt == "S_IFLNK" }
func (f FileInfo) IsReg() bool  { return f.Ifmt == "S_IFREG" || (!f.IsDir() && !f.IsLink() && f.Ifmt != "") }

type Client struct {
	conn   ios.DeviceConnectionInterface
	pktNum uint64
}

func Open(entry ios.DeviceEntry) (*Client, error) {
	conn, err := ios.ConnectToService(entry, "com.apple.afc")
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		c.conn.Close()
	}
	return nil
}

func (c *Client) send(op uint64, headerPayload, payload []byte) (afc.AfcPacket, error) {
	headerLen := uint64(len(headerPayload))
	thisLen := afc.Afc_header_size + headerLen
	entire := thisLen + uint64(len(payload))
	pkt := afc.AfcPacket{
		Header: afc.AfcPacketHeader{
			Magic: afc.Afc_magic, Packet_num: c.pktNum, Operation: op,
			This_length: thisLen, Entire_length: entire,
		},
		HeaderPayload: headerPayload,
		Payload:       payload,
	}
	c.pktNum++
	if err := afc.Encode(pkt, c.conn.Writer()); err != nil {
		return afc.AfcPacket{}, err
	}
	return afc.Decode(c.conn.Reader())
}

func (c *Client) checkStatus(pkt afc.AfcPacket) error {
	if pkt.Header.Operation != afc.Afc_operation_status {
		return nil
	}
	if len(pkt.HeaderPayload) < 8 {
		return fmt.Errorf("afc status truncated")
	}
	code := binary.LittleEndian.Uint64(pkt.HeaderPayload)
	if code == afc.Afc_Err_Success {
		return nil
	}
	if code == afc.Afc_Err_ObjectNotFound {
		return errNotFound
	}
	return fmt.Errorf("afc error %d", code)
}

var errNotFound = fmt.Errorf("ObjectNotFound")

func (c *Client) Stat(p string) (FileInfo, error) {
	resp, err := c.send(afc.Afc_operation_file_info, []byte(p), nil)
	if err != nil {
		return FileInfo{}, err
	}
	if err := c.checkStatus(resp); err != nil {
		return FileInfo{}, err
	}
	parts := bytes.Split(resp.Payload, []byte{0})
	m := map[string]string{}
	for i := 0; i+1 < len(parts); i += 2 {
		m[string(parts[i])] = string(parts[i+1])
	}
	size, _ := strconv.ParseInt(m["st_size"], 10, 64)
	target := m["st_linktarget"]
	if target == "" {
		target = m["LinkTarget"]
	}
	return FileInfo{Ifmt: m["st_ifmt"], Size: size, LinkTarget: target}, nil
}

func (c *Client) Exists(p string) (FileInfo, bool, error) {
	info, err := c.Stat(p)
	if err != nil {
		if err == errNotFound || strings.Contains(err.Error(), "ObjectNotFound") || strings.Contains(err.Error(), "not found") {
			return FileInfo{}, false, nil
		}
		return FileInfo{}, false, err
	}
	return info, true, nil
}

func (c *Client) List(p string) ([]string, error) {
	resp, err := c.send(afc.Afc_operation_read_dir, []byte(p), nil)
	if err != nil {
		return nil, err
	}
	if err := c.checkStatus(resp); err != nil {
		return nil, err
	}
	var out []string
	for _, v := range bytes.Split(resp.Payload, []byte{0}) {
		s := string(v)
		if s != "" && s != "." && s != ".." {
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *Client) MkDir(p string) error {
	payload := append([]byte(p), 0)
	resp, err := c.send(afc.Afc_operation_make_dir, payload, nil)
	if err != nil {
		return err
	}
	return c.checkStatus(resp)
}

func (c *Client) Remove(p string) error {
	resp, err := c.send(afc.Afc_operation_remove_path, []byte(p), nil)
	if err != nil {
		return err
	}
	return c.checkStatus(resp)
}

func (c *Client) openFile(p string, mode uint64) (uint64, error) {
	pathBytes := append([]byte(p), 0)
	headerPayload := make([]byte, 8+len(pathBytes))
	binary.LittleEndian.PutUint64(headerPayload, mode)
	copy(headerPayload[8:], pathBytes)
	resp, err := c.send(afc.Afc_operation_file_open, headerPayload, nil)
	if err != nil {
		return 0, err
	}
	if err := c.checkStatus(resp); err != nil {
		return 0, err
	}
	if len(resp.HeaderPayload) < 8 {
		return 0, fmt.Errorf("open: bad fd")
	}
	return binary.LittleEndian.Uint64(resp.HeaderPayload), nil
}

func (c *Client) closeFile(fd uint64) error {
	headerPayload := make([]byte, 8)
	binary.LittleEndian.PutUint64(headerPayload, fd)
	resp, err := c.send(afc.Afc_operation_file_close, headerPayload, nil)
	if err != nil {
		return err
	}
	return c.checkStatus(resp)
}

func (c *Client) GetFile(p string) ([]byte, error) {
	info, err := c.Stat(p)
	if err != nil {
		return nil, err
	}
	fd, err := c.openFile(p, afc.Afc_Mode_RDONLY)
	if err != nil {
		return nil, err
	}
	defer c.closeFile(fd)
	var buf bytes.Buffer
	left := info.Size
	for left > 0 {
		headerPayload := make([]byte, 16)
		binary.LittleEndian.PutUint64(headerPayload, fd)
		binary.LittleEndian.PutUint64(headerPayload[8:], 64*1024)
		resp, err := c.send(afc.Afc_operation_file_read, headerPayload, nil)
		if err != nil {
			return nil, err
		}
		if err := c.checkStatus(resp); err != nil {
			return nil, err
		}
		buf.Write(resp.Payload)
		left -= int64(len(resp.Payload))
		if len(resp.Payload) == 0 {
			break
		}
	}
	return buf.Bytes(), nil
}

func (c *Client) SetFile(p string, data []byte) error {
	dir := path.Dir(p)
	if dir != "." && dir != "/" {
		if err := c.MkDirs(dir); err != nil {
			return err
		}
	}
	return c.WriteToFile(bytes.NewReader(data), p)
}

func (c *Client) WriteToFile(reader io.Reader, dstPath string) error {
	fd, err := c.openFile(dstPath, afc.Afc_Mode_WR)
	if err != nil {
		return err
	}
	defer c.closeFile(fd)
	chunk := make([]byte, 64*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			headerPayload := make([]byte, 8)
			binary.LittleEndian.PutUint64(headerPayload, fd)
			resp, err2 := c.send(afc.Afc_operation_file_write, headerPayload, chunk[:n])
			if err2 != nil {
				return err2
			}
			if err2 = c.checkStatus(resp); err2 != nil {
				return err2
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) MkDirs(p string) error {
	cursor := ""
	for _, part := range strings.Split(p, "/") {
		if part == "" {
			continue
		}
		if cursor == "" {
			cursor = part
		} else {
			cursor += "/" + part
		}
		_, ok, err := c.Exists(cursor)
		if err != nil {
			return err
		}
		if !ok {
			if err := c.MkDir(cursor); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) RemoteTree(root string) (tree.Tree, error) {
	t := make(tree.Tree)
	var total int
	var visit func(abs, name string, depth int) error
	visit = func(abs, name string, depth int) error {
		if depth >= 32 || len(t) >= tree.MaxNodes {
			return fmt.Errorf("Remote tree limit")
		}
		before, err := c.Stat(abs)
		if err != nil {
			return err
		}
		switch {
		case before.IsDir():
			if name != "" {
				t[name] = tree.Node{Kind: "d"}
			}
			children, err := c.List(abs)
			if err != nil {
				return err
			}
			sort.Strings(children)
			for _, child := range children {
				if child == "" || child == "." || child == ".." || strings.Contains(child, "/") {
					return fmt.Errorf("Invalid remote name")
				}
				childName := child
				if name != "" {
					childName = name + "/" + child
				}
				if err := visit(abs+"/"+child, childName, depth+1); err != nil {
					return err
				}
			}
			after, err := c.List(abs)
			if err != nil {
				return err
			}
			sort.Strings(after)
			if !stringSlicesEqual(children, after) {
				return fmt.Errorf("Remote directory changed")
			}
		case before.IsLink():
			if name == "" {
				return fmt.Errorf("Root is a symlink")
			}
			t[name] = tree.Node{Kind: "l", Data: []byte(before.LinkTarget)}
		default:
			if name == "" || before.Size > tree.MaxBytes {
				return fmt.Errorf("Remote file limit")
			}
			data, err := c.GetFile(abs)
			if err != nil {
				return err
			}
			total += len(data)
			if total > tree.MaxBytes || int64(len(data)) != before.Size {
				return fmt.Errorf("Remote size mismatch")
			}
			t[name] = tree.Node{Kind: "f", Data: data}
		}
		after, err := c.Stat(abs)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("Remote file changed during read: %s", abs)
		}
		return nil
	}
	rootInfo, err := c.Stat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("Carrier root is not a directory")
	}
	if err := visit(root, "", 0); err != nil {
		return nil, err
	}
	if err := tree.Validate(t); err != nil {
		return nil, err
	}
	return t, nil
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SyncBooksTree reads only AirLift-related Books sync metadata (not the whole library).
// Large EPUB/PDF libraries used to trip MaxBytes via full RemoteTree("Books").
func (c *Client) SyncBooksTree() (tree.Tree, bool, error) {
	_, existed, err := c.Exists("Books")
	if err != nil {
		return nil, false, err
	}
	t := tree.Tree{}
	if !existed {
		return t, false, nil
	}
	total := 0
	for _, p := range BookDirs[1:] {
		rel := strings.TrimPrefix(p, "Books/")
		info, ok, err := c.Exists(p)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		if !info.IsDir() {
			return nil, false, fmt.Errorf("Unexpected Books directory")
		}
		t[rel] = tree.Node{Kind: "d"}
	}
	readFile := func(abs, rel string) error {
		info, ok, err := c.Exists(abs)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if !info.IsReg() {
			return fmt.Errorf("Unexpected Books sync artifact")
		}
		if info.Size > tree.MaxBytes {
			return fmt.Errorf("Remote file limit")
		}
		data, err := c.GetFile(abs)
		if err != nil {
			return err
		}
		total += len(data)
		if total > tree.MaxBytes || int64(len(data)) != info.Size {
			return fmt.Errorf("Remote size mismatch")
		}
		t[rel] = tree.Node{Kind: "f", Data: data}
		return nil
	}
	for _, p := range BookFiles {
		rel := strings.TrimPrefix(p, "Books/")
		if err := readFile(p, rel); err != nil {
			return nil, false, err
		}
	}
	for _, rel := range BookLocks {
		if err := readFile("Books/"+rel, rel); err != nil {
			return nil, false, err
		}
	}
	if err := tree.Validate(t); err != nil {
		return nil, false, err
	}
	return t, true, nil
}

func (c *Client) BooksSnapshot(runDir string) (tree.Tree, bool, error) {
	t, existed, err := c.SyncBooksTree()
	if err != nil {
		return nil, false, err
	}
	if err := tree.WriteZip(runDir+"/books.zip", t); err != nil {
		return nil, false, err
	}
	if err := run.SaveJSON(runDir+"/books.json", map[string]interface{}{
		"existed": existed, "hash": tree.Hash(t), "scope": "sync-only",
	}); err != nil {
		return nil, false, err
	}
	check, _, err := c.SyncBooksTree()
	if err != nil {
		return nil, false, err
	}
	if !tree.Equal(t, check) {
		return nil, false, fmt.Errorf("Books changed before staging")
	}
	return t, existed, nil
}

func (c *Client) RestoreBooks(t tree.Tree, existed bool) error {
	for _, p := range BookFiles {
		rel := strings.TrimPrefix(p, "Books/")
		cur, ok, err := c.Exists(p)
		if err != nil {
			return err
		}
		if ok && !cur.IsReg() {
			return fmt.Errorf("Unexpected Books artifact; keep backup")
		}
		if node, has := t[rel]; has {
			if err := c.MkDirs(path.Dir(p)); err != nil {
				return err
			}
			if err := c.SetFile(p, node.Data); err != nil {
				return err
			}
			got, err := c.GetFile(p)
			if err != nil {
				return err
			}
			if !bytes.Equal(got, node.Data) {
				return fmt.Errorf("Books restore mismatch")
			}
		} else if ok {
			if err := c.Remove(p); err != nil {
				return err
			}
		}
	}
	for _, rel := range BookLocks {
		if _, has := t[rel]; has {
			continue
		}
		p := "Books/" + rel
		node, ok, err := c.Exists(p)
		if err != nil {
			return err
		}
		if ok {
			if !node.IsReg() || node.Size != 0 {
				return fmt.Errorf("Unexpected generated Books lock; retain backup")
			}
			if err := c.Remove(p); err != nil {
				return err
			}
		}
	}
	for i := len(BookDirs) - 1; i >= 0; i-- {
		p := BookDirs[i]
		wasPresent := existed
		if p != "Books" {
			_, wasPresent = t[strings.TrimPrefix(p, "Books/")]
		}
		if !wasPresent {
			_, ok, err := c.Exists(p)
			if err != nil {
				return err
			}
			if ok {
				children, err := c.List(p)
				if err != nil {
					return err
				}
				if len(children) == 0 {
					_ = c.Remove(p)
				}
			}
		}
	}
	after, _, err := c.SyncBooksTree()
	if err != nil {
		return err
	}
	// Compare only sync artifacts we manage; ignore the rest of the Books library.
	if !tree.Equal(after, t) {
		return fmt.Errorf("Books state differs; backups retained, inspect before retry")
	}
	return nil
}

func StreamZip(entry ios.DeviceEntry, mediaSubdir string, zipData []byte) error {
	conn, err := ios.ConnectToService(entry, "com.apple.streaming_zip_conduit")
	if err != nil {
		return err
	}
	defer conn.Close()

	// Match pymobiledevice3: length-prefixed binary plist (not XML).
	payload, err := plist.Marshal(map[string]interface{}{"MediaSubdir": mediaSubdir}, plist.BinaryFormat)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := conn.Writer().Write(hdr[:]); err != nil {
		return err
	}
	if _, err := conn.Writer().Write(payload); err != nil {
		return err
	}
	if _, err := conn.Writer().Write(zipData); err != nil {
		return err
	}

	type result struct {
		reply map[string]interface{}
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		var reply map[string]interface{}
		err := readBinPlist(conn.Reader(), &reply)
		ch <- result{reply, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if status, _ := r.reply["Status"].(string); status != "DataComplete" {
			return fmt.Errorf("Streaming ZIP was rejected: %v", r.reply)
		}
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("Streaming ZIP timeout (нет DataComplete за 30с)")
	}
}

func readBinPlist(r io.Reader, v interface{}) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 16*1024*1024 {
		return fmt.Errorf("invalid plist length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	_, err := plist.Unmarshal(buf, v)
	return err
}

func BuildBooksMetadata(assets [][2]string) ([]byte, error) {
	books := make([]map[string]interface{}, 0, len(assets))
	for i, a := range assets {
		books = append(books, map[string]interface{}{
			"Persistent ID": a[0],
			"Item ID":       fmt.Sprintf("%d", i+1),
			"DSID":          "1",
		})
	}
	return plist.Marshal(map[string]interface{}{"Books": books}, plist.BinaryFormat)
}
