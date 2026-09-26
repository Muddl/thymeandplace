package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, ha := NewToken()
	b, _ := NewToken()
	if a == b || len(a) != 43 || !bytes.Equal(ha, HashToken(a)) || len(ha) != 32 {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestPassword(t *testing.T) {
	enc, err := HashPassword("correct horse battery staple")
	if err != nil || !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("enc=%q err=%v", enc, err)
	}
	if ok, _ := VerifyPassword(enc, "correct horse battery staple"); !ok {
		t.Fatal("right password rejected")
	}
	if ok, _ := VerifyPassword(enc, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("garbage", "x"); err == nil {
		t.Fatal("malformed hash must error")
	}
	other, _ := HashPassword("correct horse battery staple")
	if other == enc {
		t.Fatal("salt must differ")
	}
}

func TestSealer(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed := s.Seal([]byte("JBSWY3DPEHPK3PXP"))
	if bytes.Contains(sealed, []byte("JBSWY3DP")) {
		t.Fatal("plaintext visible")
	}
	got, err := s.Open(sealed)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q %v", got, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Open(sealed); err == nil {
		t.Fatal("tampering must be detected")
	}
	other, _ := NewSealer(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(s.Seal([]byte("x"))); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := s.Open([]byte{1, 2}); err == nil {
		t.Fatal("short input must fail")
	}
}

func TestParseSecretKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if k, err := ParseSecretKey(good); err != nil || len(k) != 32 {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := ParseSecretKey(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
