package config

import "testing"

func TestParseNetworkMap(t *testing.T) {
	got, err := ParseNetworkMap("mainnet=./data/mainnet.db, testnet=./data/testnet.db")
	if err != nil || got["mainnet"] != "./data/mainnet.db" || got["testnet"] != "./data/testnet.db" || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, value := range []string{
		"",
		"mainnet",
		"mainnet=",
		"devnet=./x.db",
		"mainnet=a,mainnet=b",
		"mainnet=a,,",
	} {
		if _, err := ParseNetworkMap(value); err == nil {
			t.Fatalf("%q accepted", value)
		}
	}
	if !IsNetworkMap("mainnet=x") || IsNetworkMap("./data/testnet.db") {
		t.Fatal("IsNetworkMap")
	}
}
