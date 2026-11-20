package cfg

import "testing"

func TestCustomCommandPrefixSectionIdentityPreservesCommandNames(t *testing.T) {
	got, err := parseCustomCommandPrefix([]byte("CUSTOMCOMMANDPREFIX:\n  Help.Topic: '#'\n  Help: '!'\n  help: '?'\n"))
	if err != nil || got["Help.Topic"] != "#" || got["Help"] != "!" || got["help"] != "?" || len(got) != 3 {
		t.Fatalf("structural case normalization changed literal command names: %v %v", got, err)
	}
	if _, err := parseCustomCommandPrefix([]byte("CUSTOMCOMMANDPREFIX: {}\ncustomCommandPrefix: {}\n")); err == nil {
		t.Fatal("ambiguous equivalent command-prefix sections accepted")
	}
}
