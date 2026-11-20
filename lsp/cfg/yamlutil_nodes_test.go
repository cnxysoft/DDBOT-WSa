package cfg

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestCredentialNodeWriterPreservesOtherSource(t *testing.T) {
	for _, tc := range []struct {
		name, before, want string
	}{
		{
			"BOM and mixed existing line endings",
			"\ufeff# header\r\nbilibili:\n  SESSDATA: old\nother: value\r\n",
			"\ufeff# header\r\nbilibili:\n  SESSDATA: \"new\"\nother: value\r\n",
		},
		{
			"single quoted keys and CRLF",
			"'BILIBILI':\r\n    'sessdata': 'old' # account\r\n    interval: 30s\r\n",
			"'BILIBILI':\r\n    'sessdata': \"new\" # account\r\n    interval: 30s\r\n",
		},
		{
			"flow with punctuation and comments",
			"# config\r\n'BILIBILI': {'sessdata': 'old, #credential', interval: 30s} # keep\r\n",
			"# config\r\n'BILIBILI': {'sessdata': \"new\", interval: 30s} # keep\r\n",
		},
		{
			"flat dotted key",
			"'BILIBILI.sessdata': old # keep\n'bilibili.bili_jct': csrf\n",
			"'BILIBILI.sessdata': \"new\" # keep\n'bilibili.bili_jct': csrf\n",
		},
		{
			"nested leaf is not the credential",
			"bilibili:\n  profile:\n    SESSDATA: nested\n  SESSDATA: old\n",
			"bilibili:\n  profile:\n    SESSDATA: nested\n  SESSDATA: \"new\"\n",
		},
		{
			"scalar text is not the credential",
			"bilibili:\n  note: |\n    SESSDATA: prose\n  SESSDATA: old\n",
			"bilibili:\n  note: |\n    SESSDATA: prose\n  SESSDATA: \"new\"\n",
		},
		{
			"insert past nested same leaf",
			"bilibili:\n  profile:\n    SESSDATA: nested\n",
			"bilibili:\n  profile:\n    SESSDATA: nested\n  SESSDATA: \"new\"\n",
		},
		{
			"insert past scalar body",
			"bilibili:\n  note: |\n    SESSDATA: prose\n",
			"bilibili:\n  note: |\n    SESSDATA: prose\n  SESSDATA: \"new\"\n",
		},
		{
			"insert preserves scalar trailing blank lines",
			"bilibili:\n  note: |+\n    SESSDATA: prose\n\n",
			"bilibili:\n  note: |+\n    SESSDATA: prose\n\n  SESSDATA: \"new\"\n",
		},
		{
			"insert in empty flow mapping",
			"BILIBILI: {} # keep\n",
			"BILIBILI: {SESSDATA: \"new\"} # keep\n",
		},
		{
			"insert in populated flow mapping",
			"BILIBILI: {bili_jct: csrf} # keep\n",
			"BILIBILI: {bili_jct: csrf, SESSDATA: \"new\"} # keep\n",
		},
		{
			"null leaf and inline comment",
			"bilibili:\n  SESSDATA: # keep\n",
			"bilibili:\n  SESSDATA: \"new\" # keep\n",
		},
		{
			"empty block section",
			"bilibili: # keep\nother: value\n",
			"bilibili: # keep\n  SESSDATA: \"new\"\nother: value\n",
		},
		{
			"flow empty leaf",
			"bilibili: {SESSDATA: , bili_jct: csrf}\n",
			"bilibili: {SESSDATA: \"new\", bili_jct: csrf}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, tc.before)
			if err := WriteConfigKeyValueToPath("bilibili.SESSDATA", "new", path); err != nil {
				t.Fatal(err)
			}
			after := readTempConfig(t, path)
			if after != tc.want {
				t.Fatalf("source changed unexpectedly:\nwant %q\ngot  %q", tc.want, after)
			}
			v := viper.New()
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(after)); err != nil || v.GetString("bilibili.SESSDATA") != "new" {
				t.Fatalf("effective value did not update: %v", err)
			}
		})
	}
}

func TestCredentialNodeWriterRejectsAmbiguityWithoutSaving(t *testing.T) {
	for _, before := range []string{
		"BILIBILI:\n  SESSDATA: one\nbilibili:\n  SESSDATA: two\n",
		"bilibili:\n  sessdata: one\n  SESSDATA: two\n",
		"bilibili.SESSDATA: flat\nbilibili:\n  SESSDATA: nested\n",
		"bilibili: &account\n  SESSDATA: old\nother: *account\n",
		"account: &account\n  SESSDATA: old\nbilibili: *account\n",
		"account: &account\n  SESSDATA: old\nbilibili:\n  <<: *account\n",
		"bilibili:\n  SESSDATA: &credential old\nother: *credential\n",
		"bilibili:\n  SESSDATA: !!str old\n",
		"bilibili:\n  SESSDATA: |\n    old\n",
		"bilibili:\n  SESSDATA: 'old\n    continued'\n",
		"bilibili:\n  SESSDATA: old\n---\nother: document\n",
	} {
		path := writeTempConfig(t, before)
		saves := 0
		err := updateConfigKeyValues(map[string]string{"bilibili.SESSDATA": "new-secret"}, path, func(data []byte, persist func() error) error {
			saves++
			return persist()
		})
		if err == nil || saves != 0 {
			t.Fatalf("unsafe source accepted: saves=%d input=%q", saves, before)
		}
		if strings.Contains(err.Error(), "new-secret") {
			t.Fatal("error revealed credential")
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(after, []byte(before)) {
			t.Fatalf("rejected write changed original: %v", readErr)
		}
	}
}

func TestCredentialWriterVerifiesAllRequestedEffectiveValues(t *testing.T) {
	before := "bilibili:\n  SESSDATA: old\n"
	path := writeTempConfig(t, before)
	if err := updateConfigKeyValues(map[string]string{
		"bilibili.SESSDATA": "new-a", "BILIBILI.sessdata": "new-b",
	}, path, nil); err == nil {
		t.Fatal("conflicting case-equivalent credential updates accepted")
	}
	if got := readTempConfig(t, path); got != before {
		t.Fatal("conflicting transaction was partially persisted")
	}
}
