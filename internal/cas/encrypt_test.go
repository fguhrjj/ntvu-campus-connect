package cas

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncryptPasswordAES128CBC(t *testing.T) {
	const salt = "abcdefghijklmnop"
	const password = "P@ss 密码1"
	// Zero bytes select alphabet[0], so both random strings are known exactly.
	reader := bytes.NewReader(bytes.Repeat([]byte{0}, 64+16))
	ciphertext, err := encryptPassword(password, salt, reader)
	if err != nil {
		t.Fatalf("encryptPassword: %v", err)
	}
	plain := decryptPasswordTest(t, ciphertext, salt, []byte("AAAAAAAAAAAAAAAA"))
	if string(plain) != strings.Repeat("A", 64)+password {
		t.Fatalf("decrypted plaintext = %q", plain)
	}
}

func TestEncryptPasswordMatchesCryptoJSVector(t *testing.T) {
	const salt = "abcdefghijklmnop"
	const iv = "BCDEFGHJKMNPQRST"
	const plain = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAz2345678z2345678z2345678z2345678P@ss 密码1"
	if len(plain) != 76 {
		t.Fatalf("test vector plaintext length = %d", len(plain))
	}
	got, err := encryptAES([]byte(plain), []byte(salt), []byte(iv))
	if err != nil {
		t.Fatalf("encryptAES: %v", err)
	}
	want := "/y+5O5akD0AjRt1kVU1S2AzzBxIGmhI6bFTk1tozBpf+GZZLrr7UPMBGNpdOBdODpjWYXZ+1IB1S/vOV2pRdeOCKhQEpCC7VPIKRcV2E9Tg="
	if got != want {
		t.Fatalf("ciphertext = %q, want %q", got, want)
	}
}

func decryptPasswordTest(t *testing.T, encoded, salt string, iv []byte) []byte {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	block, err := aes.NewCipher([]byte(salt))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	if len(ciphertext)%block.BlockSize() != 0 {
		t.Fatalf("ciphertext is not block aligned")
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > block.BlockSize() || padding > len(plain) {
		t.Fatalf("bad PKCS#7 padding: %d", padding)
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding {
			t.Fatalf("bad PKCS#7 padding byte: %d", b)
		}
	}
	return plain[:len(plain)-padding]
}
