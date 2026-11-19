package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Question mirrors the HyperShift Quest bank schema (see
// skills/hypershift-quiz/SKILL.md in openshift/hypershift). The Correct
// flags and rubrics are server-side only — they are never serialized to
// clients in the public game state.
type Question struct {
	ID       string   `yaml:"id" json:"id"`
	Topic    string   `yaml:"topic" json:"topic"`
	Depth    int      `yaml:"depth" json:"depth"`
	Format   string   `yaml:"format" json:"format"`
	Question string   `yaml:"question" json:"question"`
	Options  []Option `yaml:"options" json:"options"`
	Rubric   *Rubric  `yaml:"rubric" json:"rubric,omitempty"`
	Source   string   `yaml:"source" json:"source"`
	Why      string   `yaml:"why" json:"why"`
}

type Option struct {
	Text    string `yaml:"text" json:"text"`
	Correct bool   `yaml:"correct" json:"-"`
}

type Rubric struct {
	MustCover      []string  `yaml:"must_cover" json:"must_cover"`
	CommonMistakes []Mistake `yaml:"common_mistakes" json:"common_mistakes"`
}

type Mistake struct {
	Mistake    string `yaml:"mistake" json:"mistake"`
	Correction string `yaml:"correction" json:"correction"`
}

// Board is the 24-tile quest board layout.
type Board struct {
	Name  string `yaml:"name" json:"name"`
	Tiles []Tile `yaml:"tiles" json:"tiles"`
}

type Tile struct {
	N        int    `yaml:"n" json:"n"`
	Type     string `yaml:"type" json:"type,omitempty"`
	Category string `yaml:"category" json:"category,omitempty"`
	Format   string `yaml:"format" json:"format,omitempty"`
	Special  string `yaml:"special" json:"special,omitempty"`
}

// LoadBank loads the question bank and board. If path is empty the bank
// embedded in the binary is used. A path may point at a HyperShift
// repository checkout (the questions directory is derived) or directly at
// a directory of question YAML files.
func LoadBank(path string) ([]Question, Board, string, error) {
	if path == "" {
		qs, b, err := readBankFS(embeddedFS())
		return qs, b, "the embedded bank (sync with scripts/sync-bank.sh)", err
	}

	dir := path
	if st, err := os.Stat(filepath.Join(path, "skills", "hypershift-quiz", "questions")); err == nil && st.IsDir() {
		dir = filepath.Join(path, "skills", "hypershift-quiz", "questions")
	} else if _, err := os.Stat(dir); err != nil {
		return nil, Board{}, "", fmt.Errorf("bank path %s: %w", path, err)
	} else if !st.IsDir() {
		return nil, Board{}, "", fmt.Errorf("bank path %s is not a directory", path)
	}
	// The board layout lives next to the questions in the skill dir.
	boardDir := filepath.Dir(dir)
	if !fileExists(filepath.Join(boardDir, "board.yaml")) {
		boardDir = dir
	}

	qs, _, err := readBankFS(os.DirFS(dir))
	if err != nil {
		return nil, Board{}, "", err
	}
	var board Board
	bd, err := os.ReadFile(filepath.Join(boardDir, "board.yaml"))
	if err == nil {
		if err := yaml.Unmarshal(bd, &board); err != nil {
			return nil, Board{}, "", fmt.Errorf("board.yaml: %w", err)
		}
	}
	if len(board.Tiles) == 0 {
		board = defaultBoard()
	}
	return qs, board, dir, nil
}

func embeddedFS() fs.FS {
	sub, err := fs.Sub(embeddedBank, "bank")
	if err != nil {
		panic(err)
	}
	return sub
}

func readBankFS(fsys fs.FS) ([]Question, Board, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, Board{}, err
	}
	var qs []Question
	var board Board
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, Board{}, err
		}
		if e.Name() == "board.yaml" {
			if err := yaml.Unmarshal(data, &board); err != nil {
				return nil, Board{}, fmt.Errorf("%s: %w", e.Name(), err)
			}
			continue
		}
		var file []Question
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, Board{}, fmt.Errorf("%s: %w", e.Name(), err)
		}
		qs = append(qs, file...)
	}
	if len(board.Tiles) == 0 {
		board = defaultBoard()
	}
	sort.Slice(qs, func(i, j int) bool { return qs[i].ID < qs[j].ID })
	return qs, board, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// defaultBoard is the canonical quest board, used when no board.yaml is
// present. It matches skills/hypershift-quiz/board.yaml upstream.
func defaultBoard() Board {
	tiles := []Tile{{N: 1, Type: "start"}, {N: 24, Type: "finish"}, {N: 7, Type: "wild"}}
	specs := []struct {
		n                int
		category, format string
		special          string
	}{
		{2, "architecture", "mcq", ""}, {3, "networking", "mcq", ""},
		{4, "control-plane", "explain", ""}, {5, "data-plane", "mcq", ""},
		{6, "lifecycle", "mcq", ""}, {8, "architecture", "code", ""},
		{9, "networking", "explain", ""}, {10, "kubevirt", "mcq", ""},
		{11, "control-plane", "mcq", ""}, {12, "lifecycle", "explain", ""},
		{13, "architecture", "mcq", "challenge"}, {14, "kubevirt", "code", ""},
		{15, "control-plane", "mcq", ""}, {16, "data-plane", "explain", ""},
		{17, "lifecycle", "code", ""}, {18, "kubevirt", "explain", ""},
		{19, "networking", "scenario", ""}, {20, "data-plane", "scenario", ""},
		{21, "control-plane", "explain", "challenge"}, {22, "lifecycle", "mcq", ""},
		{23, "kubevirt", "scenario", ""},
	}
	for _, s := range specs {
		tiles = append(tiles, Tile{N: s.n, Category: s.category, Format: s.format, Special: s.special})
	}
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].N < tiles[j].N })
	return Board{Name: "HyperShift Quest", Tiles: tiles}
}
