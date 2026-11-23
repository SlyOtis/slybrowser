package slyb

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	snssSetTabWindow        = 0
	snssSetTabIndexInWindow = 2
	snssUpdateTabNavigation = 6
	snssSetSelectedNavIndex = 7
	snssSetPinnedState      = 12
	snssTabClosed           = 16
	snssWindowClosed        = 17
)

type snssTab struct {
	window   int32
	index    int32
	selected int32
	pinned   bool
	closed   bool
	pages    map[int32]Page
}

type SessionTab struct {
	Page
	Window int32
	Pinned bool
}

type pickle struct {
	data []byte
	at   int
}

func (p *pickle) int32() (int32, bool) {
	if p.at+4 > len(p.data) {
		return 0, false
	}
	value := int32(binary.LittleEndian.Uint32(p.data[p.at:]))
	p.at += 4
	return value, true
}

func (p *pickle) align() {
	p.at = (p.at + 3) &^ 3
}

func (p *pickle) string() (string, bool) {
	length, ok := p.int32()
	if !ok || length < 0 || p.at+int(length) > len(p.data) {
		return "", false
	}
	value := string(p.data[p.at : p.at+int(length)])
	p.at += int(length)
	p.align()
	return value, true
}

func (p *pickle) string16() (string, bool) {
	length, ok := p.int32()
	if !ok || length < 0 || p.at+int(length)*2 > len(p.data) {
		return "", false
	}
	units := make([]uint16, length)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(p.data[p.at+i*2:])
	}
	p.at += int(length) * 2
	p.align()
	return string(utf16.Decode(units)), true
}

func GetSessionTabs(path string) ([]SessionTab, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 8 || string(raw[:4]) != "SNSS" {
		return nil, nil
	}
	tabs := map[int32]*snssTab{}
	closedWindows := map[int32]bool{}
	tab := func(id int32) *snssTab {
		if tabs[id] == nil {
			tabs[id] = &snssTab{selected: -1, pages: map[int32]Page{}}
		}
		return tabs[id]
	}
	for at := 8; at+3 <= len(raw); {
		size := int(binary.LittleEndian.Uint16(raw[at:]))
		if size == 0 || at+2+size > len(raw) {
			break
		}
		id := raw[at+2]
		payload := raw[at+3 : at+2+size]
		at += 2 + size
		pair := func() (int32, int32, bool) {
			if len(payload) < 8 {
				return 0, 0, false
			}
			return int32(binary.LittleEndian.Uint32(payload)), int32(binary.LittleEndian.Uint32(payload[4:])), true
		}
		switch id {
		case snssUpdateTabNavigation:
			if len(payload) < 4 {
				continue
			}
			reader := pickle{data: payload, at: 4}
			tabID, ok1 := reader.int32()
			index, ok2 := reader.int32()
			url, ok3 := reader.string()
			title, ok4 := reader.string16()
			if ok1 && ok2 && ok3 && ok4 {
				tab(tabID).pages[index] = Page{URL: url, Title: title}
			}
		case snssSetSelectedNavIndex:
			if tabID, index, ok := pair(); ok {
				tab(tabID).selected = index
			}
		case snssSetTabWindow:
			if window, tabID, ok := pair(); ok {
				tab(tabID).window = window
			}
		case snssSetTabIndexInWindow:
			if tabID, index, ok := pair(); ok {
				tab(tabID).index = index
			}
		case snssSetPinnedState:
			if len(payload) >= 5 {
				tab(int32(binary.LittleEndian.Uint32(payload))).pinned = payload[4] != 0
			}
		case snssTabClosed:
			if len(payload) >= 4 {
				tab(int32(binary.LittleEndian.Uint32(payload))).closed = true
			}
		case snssWindowClosed:
			if len(payload) >= 4 {
				closedWindows[int32(binary.LittleEndian.Uint32(payload))] = true
			}
		}
	}
	var open []*snssTab
	for _, entry := range tabs {
		if !entry.closed && !closedWindows[entry.window] && len(entry.pages) > 0 {
			open = append(open, entry)
		}
	}
	sort.Slice(open, func(i, j int) bool {
		if open[i].window != open[j].window {
			return open[i].window < open[j].window
		}
		return open[i].index < open[j].index
	})
	var result []SessionTab
	seen := map[string]bool{}
	for _, entry := range open {
		page, ok := entry.pages[entry.selected]
		if !ok {
			last := int32(-1)
			for index := range entry.pages {
				if index > last {
					last = index
				}
			}
			page = entry.pages[last]
		}
		if !strings.HasPrefix(page.URL, "http://") && !strings.HasPrefix(page.URL, "https://") || seen[page.URL] {
			continue
		}
		seen[page.URL] = true
		result = append(result, SessionTab{Page: page, Window: entry.window, Pinned: entry.pinned})
	}
	return result, nil
}

func GetNewestSession(dir string) (string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "Session_*"))
	if err != nil || len(files) == 0 {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool {
		a, _ := os.Stat(files[i])
		b, _ := os.Stat(files[j])
		return a.ModTime().After(b.ModTime())
	})
	return files[0], nil
}
