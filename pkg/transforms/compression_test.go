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
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/assert"
)

const (
	clearString = "This is the test string used for testing"
	gzipString  = "H4sIAAAJbogA/wrJyCxWyCxWKMlIVShJLS5RKC4pysxLVygtTk1RSMsvAgtm5qUDAgAA//8tdaMdKAAAAA=="
	zlibString  = "eJwKycgsVsgsVijJSFUoSS0uUSguKcrMS1coLU5NUUjLLwILZualAwIAAP//KucO4w=="
)

func TestGzip(t *testing.T) {

	comp := NewCompression()
	continuePipeline, result := comp.CompressWithGZIP(context, []byte(clearString))
	assert.True(t, continuePipeline)

	compressed, err := base64.StdEncoding.DecodeString(string(result.([]byte)))
	require.NoError(t, err)

	var buf bytes.Buffer
	buf.Write(compressed)

	zr, err := gzip.NewReader(&buf)
	require.NoError(t, err)

	decoded, err := ioutil.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, clearString, string(decoded))

	continuePipeline2, result2 := comp.CompressWithGZIP(context, []byte(clearString))
	assert.True(t, continuePipeline2)
	assert.Equal(t, result.([]byte), result2.([]byte))
}

func TestZlib(t *testing.T) {

	comp := NewCompression()
	continuePipeline, result := comp.CompressWithZLIB(context, []byte(clearString))
	assert.True(t, continuePipeline)
	require.NotNil(t, result)

	compressed, err := base64.StdEncoding.DecodeString(string(result.([]byte)))
	require.NoError(t, err)

	var buf bytes.Buffer
	buf.Write(compressed)

	zr, err := zlib.NewReader(&buf)
	require.NoError(t, err)

	decoded, err := ioutil.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, clearString, string(decoded))

	continuePipeline2, result2 := comp.CompressWithZLIB(context, []byte(clearString))
	assert.True(t, continuePipeline2)
	assert.Equal(t, result.([]byte), result2.([]byte))
}

var result []byte

func BenchmarkGzip(b *testing.B) {

	comp := NewCompression()

	var enc interface{}
	for i := 0; i < b.N; i++ {
		_, enc = comp.CompressWithGZIP(context, []byte(clearString))
	}
	b.SetBytes(int64(len(enc.([]byte))))
	result = enc.([]byte)
}

func BenchmarkZlib(b *testing.B) {

	comp := NewCompression()

	var enc interface{}
	for i := 0; i < b.N; i++ {
		_, enc = comp.CompressWithZLIB(context, []byte(clearString))
	}
	b.SetBytes(int64(len(enc.([]byte))))
	result = enc.([]byte)
}
