package console

import (
	"strings"
	"testing"
)

func TestTableLinesUpColumnsByTheirWidthOnScreen(t *testing.T) {
	var tab Table
	tab.Row("設定", "/etc/goft/job.yaml", "OK")
	tab.Row("ローカル", "/data", "OK  読み書き可能")
	tab.Row("config", "x", "")
	var b strings.Builder
	if _, err := tab.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	want := "設定      /etc/goft/job.yaml  OK\n" +
		"ローカル  /data               OK  読み書き可能\n" +
		"config    x\n"
	if b.String() != want {
		t.Errorf("table =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestATrailingCellDoesNotWidenItsColumn(t *testing.T) {
	// A warning has nothing after it, so its length is no reason to push the
	// status of every other row out to meet it.
	var tab Table
	tab.Row("local", "/data", "OK")
	tab.Row("warning", "a very long warning that would otherwise set the width of the column", "")
	var b strings.Builder
	_, _ = tab.WriteTo(&b)
	if first := strings.Split(b.String(), "\n")[0]; first != "local    /data  OK" {
		t.Errorf("first row = %q, want the status next to the short path", first)
	}
}

func TestTableStartsAfreshAfterWriting(t *testing.T) {
	var tab Table
	tab.Row("a", "first", "OK")
	var b strings.Builder
	_, _ = tab.WriteTo(&b)
	tab.Row("bb", "second", "OK")
	_, _ = tab.WriteTo(&b)
	if b.String() != "a  first  OK\nbb  second  OK\n" {
		t.Errorf("output = %q", b.String())
	}
}
