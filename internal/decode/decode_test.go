package decode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecodeGoldenVectors decodes every captured testdata/<name>.enc (real
// encrypted responses from apiv3.shanbay.com) and asserts byte-exact equality
// with testdata/<name>.json, the plaintext produced by the reference JS decoder
// (spike/dec.mjs). It also confirms each result parses as JSON.
func TestDecodeGoldenVectors(t *testing.T) {
	encFiles, err := filepath.Glob("testdata/*.enc")
	if err != nil {
		t.Fatal(err)
	}
	if len(encFiles) == 0 {
		t.Fatal("no .enc golden vectors found in testdata")
	}
	for _, encFile := range encFiles {
		name := strings.TrimSuffix(filepath.Base(encFile), ".enc")
		t.Run(name, func(t *testing.T) {
			enc := readTrim(t, encFile)
			want := readTrim(t, "testdata/"+name+".json")

			got, err := Decode(enc)
			if err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if strings.TrimRight(got, "\r\n") != want {
				t.Fatalf("decode mismatch:\n got: %q\nwant: %q", got, want)
			}
			var v any
			if err := json.Unmarshal([]byte(got), &v); err != nil {
				t.Fatalf("decoded output is not valid JSON: %v\noutput: %s", err, got)
			}
		})
	}
}

// TestDecodeRejectsPlainJSON ensures unencoded bodies (e.g. error responses)
// are rejected via the version check rather than silently mis-decoded.
func TestDecodeRejectsPlainJSON(t *testing.T) {
	for _, body := range []string{
		`{"errors":{},"msg":"learning data not ready"}`,
		`{"message":"method not allowed"}`,
		``,
		`abc`,
	} {
		if _, err := Decode(body); err == nil {
			t.Errorf("expected error for plain/short input %q, got nil", body)
		}
	}
}

func readTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimRight(string(b), "\r\n")
}
