package netease

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"strings"
)

const (
	presetKey   = "0CoJUm6Qyw8W8jud"
	linuxAPIKey = "rFgB&h#%2?^eDg:Q"
	eapiKey     = "e82ckenh8dichen8"
	iv          = "0102030405060708"
	base62      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

const rsaPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDgtQn2JZ34ZC28NWYpAUd98iZ37BUrX/aKzmFbt7clFSs6sXqHauqKWqdtLkF2KexO40H1YTX8z2lSgBBOAxLsvaklV8k4cBFK9snQXE9/DDaFt6Rr7iVZMldczhC0JNgTz+SHXT6CBHuX3e9SdB1Ua44oncaTWz7OBGLbCiK45wIDAQAB
-----END PUBLIC KEY-----`

var rsaPubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(rsaPublicKeyPEM))
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("netease: parse RSA public key: " + err.Error())
	}
	rsaPubKey = pub.(*rsa.PublicKey)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding > aes.BlockSize {
		return data
	}
	return data[:len(data)-padding]
}

func aesEncryptCBC(plaintext, key, ivBytes []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, ivBytes)
	mode.CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

func aesEncryptECB(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(ciphertext[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return ciphertext, nil
}

func aesDecryptECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("netease: ciphertext not multiple of block size")
	}
	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += aes.BlockSize {
		block.Decrypt(plaintext[i:i+aes.BlockSize], ciphertext[i:i+aes.BlockSize])
	}
	return pkcs7Unpad(plaintext), nil
}

// rsaEncryptNoPadding 使用 RSA "NONE" padding（原始模幂运算）加密。
func rsaEncryptNoPadding(text string) string {
	plainBytes := []byte(text)
	c := new(big.Int).SetBytes(plainBytes)
	encrypted := c.Exp(c, big.NewInt(int64(rsaPubKey.E)), rsaPubKey.N)
	hexStr := fmt.Sprintf("%0256x", encrypted)
	return hexStr
}

func randomBase62(n int) string {
	b := make([]byte, n)
	for i := range b {
		rb := make([]byte, 1)
		_, _ = rand.Read(rb)
		b[i] = base62[int(rb[0])%62]
	}
	return string(b)
}

func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// WeAPI 双重 AES-CBC 加密 + RSA 密钥传输。
type WeAPIParams struct {
	Params    string
	EncSecKey string
}

func WeAPIEncrypt(jsonData []byte) (*WeAPIParams, error) {
	// 第一次 AES-CBC with presetKey
	first, err := aesEncryptCBC(jsonData, []byte(presetKey), []byte(iv))
	if err != nil {
		return nil, err
	}
	firstB64 := base64.StdEncoding.EncodeToString(first)

	// 生成 16 位随机 secretKey
	secretKey := randomBase62(16)

	// 第二次 AES-CBC with secretKey
	second, err := aesEncryptCBC([]byte(firstB64), []byte(secretKey), []byte(iv))
	if err != nil {
		return nil, err
	}
	params := base64.StdEncoding.EncodeToString(second)

	// RSA 加密 reversed secretKey
	encSecKey := rsaEncryptNoPadding(reverseString(secretKey))

	return &WeAPIParams{Params: params, EncSecKey: encSecKey}, nil
}

// LinuxAPIEncrypt AES-128-ECB 加密。
func LinuxAPIEncrypt(jsonData []byte) string {
	encrypted, _ := aesEncryptECB(jsonData, []byte(linuxAPIKey))
	return strings.ToUpper(hex.EncodeToString(encrypted))
}

// EAPIEncrypt EAPI 加密（URL + Data + MD5 签名）。
func EAPIEncrypt(url string, jsonData []byte) string {
	text := string(jsonData)
	message := fmt.Sprintf("nobody%suse%smd5forencrypt", url, text)
	digest := fmt.Sprintf("%x", md5.Sum([]byte(message)))
	data := fmt.Sprintf("%s-36cd479b6b5-%s-36cd479b6b5-%s", url, text, digest)
	encrypted, _ := aesEncryptECB([]byte(data), []byte(eapiKey))
	return strings.ToUpper(hex.EncodeToString(encrypted))
}

// EAPIDecrypt 解密 EAPI 响应。
func EAPIDecrypt(encrypted []byte, isGzipped bool) ([]byte, error) {
	decrypted, err := aesDecryptECB(encrypted, []byte(eapiKey))
	if err != nil {
		return nil, err
	}
	if isGzipped {
		reader, err := gzip.NewReader(bytes.NewReader(decrypted))
		if err != nil {
			return decrypted, nil
		}
		defer reader.Close()
		return io.ReadAll(reader)
	}
	return decrypted, nil
}
