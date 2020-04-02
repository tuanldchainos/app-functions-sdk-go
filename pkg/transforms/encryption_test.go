//
// Copyright (c) 2017 Cavium
// Copyright (c) 2019 Intel Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package transforms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"

	"testing"

	"github.com/edgexfoundry/go-mod-core-contracts/models"
	"github.com/stretchr/testify/assert"
)

const (
	plainString = "This is the test string used for testing"
	iv          = "123456789012345678901234567890"
	key         = "aquqweoruqwpeoruqwpoeruqwpoierupqoweiurpoqwiuerpqowieurqpowieurpoqiweuroipwqure"
)

func aesDecrypt(crypt []byte, aesData models.EncryptionDetails) []byte {
	hash := sha1.New()

	hash.Write([]byte((aesData.Key)))
	key := hash.Sum(nil)
	key = key[:blockSize]

	iv := make([]byte, blockSize)
	copy(iv, []byte(aesData.InitVector))

	block, err := aes.NewCipher(key)
	if err != nil {
		panic("key error")
	}

	decodedData, _ := base64.StdEncoding.DecodeString(string(crypt))

	ecb := cipher.NewCBCDecrypter(block, []byte(iv))
	decrypted := make([]byte, len(decodedData))
	ecb.CryptBlocks(decrypted, decodedData)

	trimmed := pkcs5Trimming(decrypted)

	return trimmed
}

func pkcs5Trimming(encrypt []byte) []byte {
	padding := encrypt[len(encrypt)-1]
	return encrypt[:len(encrypt)-int(padding)]
}

func TestAES(t *testing.T) {

	aesData := models.EncryptionDetails{
		Algo:       "AES",
		Key:        key,
		InitVector: iv,
	}

	enc := NewEncryption(aesData.Key, aesData.InitVector)

	continuePipeline, cphrd := enc.EncryptWithAES(context, []byte(plainString))
	assert.True(t, continuePipeline)

	decphrd := aesDecrypt(cphrd.([]byte), aesData)

	assert.Equal(t, string(plainString), string(decphrd))
}

func TestAESNoData(t *testing.T) {
	aesData := models.EncryptionDetails{
		Algo:       "AES",
		Key:        key,
		InitVector: iv,
	}

	enc := NewEncryption(aesData.Key, aesData.InitVector)

	continuePipeline, result := enc.EncryptWithAES(context)
	assert.False(t, continuePipeline)
	assert.Error(t, result.(error), "expect an error")
}
