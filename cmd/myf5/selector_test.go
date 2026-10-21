package main

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSelectFallback_BasicSelection(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "Option Alpha", Description: "First option"},
		{ID: "2", Title: "Option Beta", Description: "Second option"},
		{ID: "3", Title: "Option Gamma", Description: "Third option"},
	}

	// 1. Select by number "2"
	in := strings.NewReader("2\n")
	var out bytes.Buffer
	cfg := SelectConfig{
		Title:        "Choose an option",
		Items:        items,
		DefaultIndex: 0,
		AllowBack:    true,
		In:           in,
		Out:          &out,
		Reader:       bufio.NewReader(in),
	}

	idx, chosen, err := SelectMenu(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 || chosen.Title != "Option Beta" {
		t.Errorf("expected index 1 (Option Beta), got %d (%s)", idx, chosen.Title)
	}
	if !strings.Contains(out.String(), "Choose an option") {
		t.Errorf("expected title in output")
	}

	// 2. Select default by pressing Enter (empty input)
	in2 := strings.NewReader("\n")
	var out2 bytes.Buffer
	cfg2 := SelectConfig{
		Title:        "Choose default",
		Items:        items,
		DefaultIndex: 2, // Gamma
		AllowBack:    true,
		In:           in2,
		Out:          &out2,
		Reader:       bufio.NewReader(in2),
	}
	idx2, chosen2, err2 := SelectMenu(cfg2)
	if err2 != nil {
		t.Fatalf("unexpected error on default: %v", err2)
	}
	if idx2 != 2 || chosen2.Title != "Option Gamma" {
		t.Errorf("expected index 2 (Option Gamma), got %d (%s)", idx2, chosen2.Title)
	}

	// 3. Retry on invalid input then choose "1"
	in3 := strings.NewReader("99\nfoo\n1\n")
	var out3 bytes.Buffer
	cfg3 := SelectConfig{
		Title:     "Retry test",
		Items:     items,
		AllowBack: false,
		In:        in3,
		Out:       &out3,
		Reader:    bufio.NewReader(in3),
	}
	idx3, chosen3, err3 := SelectMenu(cfg3)
	if err3 != nil {
		t.Fatalf("unexpected error on retry: %v", err3)
	}
	if idx3 != 0 || chosen3.Title != "Option Alpha" {
		t.Errorf("expected index 0 (Option Alpha), got %d (%s)", idx3, chosen3.Title)
	}
	if !strings.Contains(out3.String(), "Invalid selection") {
		t.Errorf("expected invalid selection notice in output")
	}
}

func TestSelectFallback_BackAndQuit(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
	}

	// Back with 'b'
	inBack := strings.NewReader("b\n")
	var outBack bytes.Buffer
	cfgBack := SelectConfig{
		Items:     items,
		AllowBack: true,
		In:        inBack,
		Out:       &outBack,
		Reader:    bufio.NewReader(inBack),
	}
	_, _, errBack := SelectMenu(cfgBack)
	if !errors.Is(errBack, ErrSelectBack) {
		t.Errorf("expected ErrSelectBack, got %v", errBack)
	}

	// Quit with 'q'
	inQuit := strings.NewReader("q\n")
	var outQuit bytes.Buffer
	cfgQuit := SelectConfig{
		Items:     items,
		AllowBack: true,
		In:        inQuit,
		Out:       &outQuit,
		Reader:    bufio.NewReader(inQuit),
	}
	_, _, errQuit := SelectMenu(cfgQuit)
	if !errors.Is(errQuit, ErrSelectQuit) {
		t.Errorf("expected ErrSelectQuit, got %v", errQuit)
	}
}

func TestReadKey_Decoding(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected KeyType
		expRune  rune
	}{
		{"Up Arrow CSI", []byte{0x1b, '[', 'A'}, KeyUp, 0},
		{"Down Arrow CSI", []byte{0x1b, '[', 'B'}, KeyDown, 0},
		{"Right Arrow CSI", []byte{0x1b, '[', 'C'}, KeyRight, 0},
		{"Left Arrow CSI", []byte{0x1b, '[', 'D'}, KeyLeft, 0},
		{"Home CSI", []byte{0x1b, '[', 'H'}, KeyHome, 0},
		{"End CSI", []byte{0x1b, '[', 'F'}, KeyEnd, 0},
		{"PageUp CSI", []byte{0x1b, '[', '5', '~'}, KeyPageUp, 0},
		{"PageDown CSI", []byte{0x1b, '[', '6', '~'}, KeyPageDown, 0},
		{"Up Arrow SS3", []byte{0x1b, 'O', 'A'}, KeyUp, 0},
		{"Down Arrow SS3", []byte{0x1b, 'O', 'B'}, KeyDown, 0},
		{"Enter CR", []byte{'\r'}, KeyEnter, 0},
		{"Enter LF", []byte{'\n'}, KeyEnter, 0},
		{"Ctrl-C", []byte{3}, KeyQuit, 0},
		{"Ctrl-D", []byte{4}, KeyQuit, 0},
		{"Backspace DEL", []byte{127}, KeyBackspace, 0},
		{"Backspace BS", []byte{8}, KeyBackspace, 0},
		{"Tab", []byte{9}, KeyTab, 0},
		{"Standalone Escape", []byte{0x1b}, KeyBack, 0},
		{"Plain Rune 'k'", []byte{'k'}, KeyRune, 'k'},
		{"Plain Rune 'j'", []byte{'j'}, KeyRune, 'j'},
		{"Plain Rune '/'", []byte{'/'}, KeyRune, '/'},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bufio.NewReader(bytes.NewReader(tc.input))
			key, err := readKey(r)
			if err != nil {
				t.Fatalf("unexpected readKey error: %v", err)
			}
			if key.Type != tc.expected {
				t.Errorf("expected KeyType %v, got %v", tc.expected, key.Type)
			}
			if tc.expRune != 0 && key.Rune != tc.expRune {
				t.Errorf("expected rune %c, got %c", tc.expRune, key.Rune)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	s := "BIG-IP 17.5.1-0.0.12.iso"
	if got := truncateRunes(s, 30); got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
	if got := truncateRunes(s, 10); got != "BIG-IP ..." {
		t.Errorf("expected 'BIG-IP ...', got %q", got)
	}
}

func TestInteractiveLoop_ArrowNavigation(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
		{ID: "3", Title: "Third"},
		{ID: "4", Title: "Fourth"},
	}

	// Press Down arrow twice, then Enter
	// Sequence: \x1b[B, \x1b[B, \r
	keySeq := []byte{
		0x1b, '[', 'B', // Down -> item 2
		0x1b, '[', 'B', // Down -> item 3
		'\r', // Enter
	}
	in := bytes.NewReader(keySeq)
	var out bytes.Buffer

	cfg := SelectConfig{
		Title:        "Select One",
		Items:        items,
		DefaultIndex: 0,
		In:           in,
		Out:          &out,
	}

	idx, chosen, err := runInteractiveLoop(cfg, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 || chosen.Title != "Third" {
		t.Errorf("expected index 2 ('Third'), got %d (%s)", idx, chosen.Title)
	}

	// Test j/k navigation: j (down), j (down), k (up), Enter
	keySeqJK := []byte{'j', 'j', 'k', '\n'}
	inJK := bytes.NewReader(keySeqJK)
	var outJK bytes.Buffer

	cfgJK := SelectConfig{
		Items:        items,
		DefaultIndex: 0,
		In:           inJK,
		Out:          &outJK,
	}
	idxJK, chosenJK, errJK := runInteractiveLoop(cfgJK, 0, 0)
	if errJK != nil {
		t.Fatalf("unexpected error on j/k: %v", errJK)
	}
	if idxJK != 1 || chosenJK.Title != "Second" {
		t.Errorf("expected index 1 ('Second'), got %d (%s)", idxJK, chosenJK.Title)
	}
}

func TestInteractiveLoop_ScrollingAndViewport(t *testing.T) {
	// Create 25 items
	var items []SelectItem
	for i := 1; i <= 25; i++ {
		items = append(items, SelectItem{
			ID:    string(rune('A' + i - 1)),
			Title: "Item " + string(rune('A'+i-1)),
		})
	}

	// Navigate with PageDown and Enter
	// Sequence: \x1b[6~ (PageDown), \r (Enter)
	keySeq := []byte{
		0x1b, '[', '6', '~', // PageDown -> jumps by maxVisible
		'\r',
	}
	in := bytes.NewReader(keySeq)
	var out bytes.Buffer

	cfg := SelectConfig{
		Title:        "Scroll Test",
		Items:        items,
		DefaultIndex: 0,
		MaxVisible:   5,
		In:           in,
		Out:          &out,
	}

	idx, _, err := runInteractiveLoop(cfg, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx < 5 {
		t.Errorf("expected selection after PageDown to be >= 5, got %d", idx)
	}

	// Output should contain scroll indicators
	outStr := out.String()
	if !strings.Contains(outStr, "more below") {
		t.Errorf("expected 'more below' scroll indicator in output")
	}
}

func TestInteractiveLoop_FilterAndSearch(t *testing.T) {
	items := []SelectItem{
		{ID: "iso", Title: "BIG-IP 17.5.1 Base ISO"},
		{ID: "hf", Title: "BIG-IP 17.5.1 Hotfix 1"},
		{ID: "sig", Title: "Security Attack Signatures Update"},
		{ID: "qkview", Title: "iHealth QKView Diagnostic Tool"},
	}

	// Sequence:
	// '/' (start filter), 's', 'i', 'g' (type "sig"), \r (Enter)
	keySeq := []byte{
		'/', 's', 'i', 'g', '\r',
	}
	in := bytes.NewReader(keySeq)
	var out bytes.Buffer

	cfg := SelectConfig{
		Title:       "Filter Test",
		Items:       items,
		AllowFilter: true,
		In:          in,
		Out:         &out,
	}

	idx, chosen, err := runInteractiveLoop(cfg, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 || chosen.ID != "sig" {
		t.Errorf("expected filtered item 2 ('sig'), got %d (%s)", idx, chosen.Title)
	}
}

func TestInteractiveLoop_HomeEnd(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "First"},
		{ID: "2", Title: "Second"},
		{ID: "3", Title: "Third"},
		{ID: "4", Title: "Fourth"},
	}

	// End (\x1b[F), then Enter (\r) -> should select last item (Fourth)
	keySeq := []byte{0x1b, '[', 'F', '\r'}
	in := bytes.NewReader(keySeq)
	var out bytes.Buffer

	cfg := SelectConfig{
		Items:        items,
		DefaultIndex: 0,
		In:           in,
		Out:          &out,
	}
	idx, chosen, err := runInteractiveLoop(cfg, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 3 || chosen.Title != "Fourth" {
		t.Errorf("expected last item ('Fourth'), got %d (%s)", idx, chosen.Title)
	}
}

func TestInteractiveLoop_NumberJumping(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "Item 1"},
		{ID: "2", Title: "Item 2"},
		{ID: "3", Title: "Item 3"},
	}

	// Press '2', then Enter (\r) -> jumps directly to Item 2
	keySeq := []byte{'2', '\r'}
	in := bytes.NewReader(keySeq)
	var out bytes.Buffer

	cfg := SelectConfig{
		Items:        items,
		DefaultIndex: 0,
		In:           in,
		Out:          &out,
	}
	idx, chosen, err := runInteractiveLoop(cfg, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 || chosen.Title != "Item 2" {
		t.Errorf("expected index 1 ('Item 2'), got %d (%s)", idx, chosen.Title)
	}
}

func TestInteractiveLoop_BackAndQuit(t *testing.T) {
	items := []SelectItem{
		{ID: "1", Title: "Item 1"},
	}

	// 'b' -> ErrSelectBack
	inBack := bytes.NewReader([]byte{'b'})
	var outBack bytes.Buffer
	cfgBack := SelectConfig{
		Items:     items,
		AllowBack: true,
		In:        inBack,
		Out:       &outBack,
	}
	_, _, errBack := runInteractiveLoop(cfgBack, 0, 0)
	if !errors.Is(errBack, ErrSelectBack) {
		t.Errorf("expected ErrSelectBack, got %v", errBack)
	}

	// 'q' -> ErrSelectQuit
	inQuit := bytes.NewReader([]byte{'q'})
	var outQuit bytes.Buffer
	cfgQuit := SelectConfig{
		Items:     items,
		AllowBack: true,
		In:        inQuit,
		Out:       &outQuit,
	}
	_, _, errQuit := runInteractiveLoop(cfgQuit, 0, 0)
	if !errors.Is(errQuit, ErrSelectQuit) {
		t.Errorf("expected ErrSelectQuit, got %v", errQuit)
	}
}
