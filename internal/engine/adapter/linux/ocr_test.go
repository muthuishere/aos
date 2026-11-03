//go:build linux

package linux

import "testing"

func TestParseTesseractTSV(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"4\t1\t1\t1\t1\t0\t10\t20\t100\t15\t-1\t\n" +
		"5\t1\t1\t1\t1\t1\t10\t20\t40\t15\t95\tHello\n" +
		"5\t1\t1\t1\t1\t2\t60\t21\t50\t14\t95\tworld\n" +
		"5\t1\t1\t1\t2\t1\t10\t50\t30\t15\t90\tnext\n"
	got := parseTesseractTSV(tsv, 100, 200)
	if len(got) != 2 || got[0].Text != "Hello world" || got[1].Text != "next" {
		t.Fatalf("lines = %+v", got)
	}
	if b := got[0].Bounds; b.X != 110 || b.Y != 220 || b.W != 100 || b.H != 15 {
		t.Fatalf("bounds = %+v", b)
	}
	if len(got[0].Words) != 2 {
		t.Fatalf("words = %d", len(got[0].Words))
	}
}
