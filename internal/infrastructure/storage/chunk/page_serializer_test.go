// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package chunk

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jzhang405/NexKV/internal/infrastructure/storage/offheap"
)

func TestPageSerializer_Roundtrip_LeafPage(t *testing.T) {
	require.NoError(t, offheap.InitPageManager(64*1024))
	pm := offheap.GetPageManager()
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa := offheap.NewPageAccessor(pm)
	pa.InitLeafPage(pageID, 1)

	dataEnd := uint16(offheap.SizeofPageHeader)
	pa.InsertLeafEntry(pageID, 0, []byte("hello"), []byte("world"), &dataEnd)
	pa.InsertLeafEntry(pageID, 1, []byte("foo"), []byte("bar"), &dataEnd)
	pa.InsertLeafEntry(pageID, 2, []byte("nxkv"), []byte("p4"), &dataEnd)

	ptr := pm.PageIDToPtr(pageID)
	pageLength := int(dataEnd)

	serializer := &PageSerializer{}
	data, err := serializer.Serialize(ptr, pageLength)
	require.NoError(t, err)
	require.NotNil(t, data)
	assert.Equal(t, CRCSize+pageLength, len(data))

	dstID, err := pm.Alloc()
	require.NoError(t, err)
	dstPtr := pm.PageIDToPtr(dstID)
	decoded, err := serializer.Deserialize(data, dstPtr)
	require.NoError(t, err)
	assert.Equal(t, pageLength, decoded)
	assert.True(t, pa.IsLeaf(dstID))
	assert.Equal(t, uint16(3), pa.GetCount(dstID))
}

func TestPageSerializer_Roundtrip_IndexPage(t *testing.T) {
	require.NoError(t, offheap.InitPageManager(64*1024))
	pm := offheap.GetPageManager()
	pageID, err := pm.Alloc()
	require.NoError(t, err)

	pa := offheap.NewPageAccessor(pm)
	pa.InitIndexPage(pageID, 1)

	dataEnd := uint16(offheap.SizeofPageHeader)
	pa.InsertIndexEntry(pageID, 0, []byte("key1"), 42, &dataEnd)
	pa.InsertIndexEntry(pageID, 1, []byte("key2"), 43, &dataEnd)
	pa.SetChild(pageID, 2, 44)

	ptr := pm.PageIDToPtr(pageID)
	pageLength := int(dataEnd)

	serializer := &PageSerializer{}
	data, err := serializer.Serialize(ptr, pageLength)
	require.NoError(t, err)

	dstID, _ := pm.Alloc()
	dstPtr := pm.PageIDToPtr(dstID)
	decoded, err := serializer.Deserialize(data, dstPtr)
	require.NoError(t, err)
	assert.Equal(t, pageLength, decoded)
	assert.False(t, pa.IsLeaf(dstID))
}

func TestPageSerializer_Serialize_InvalidLength(t *testing.T) {
	require.NoError(t, offheap.InitPageManager(64*1024))
	pm := offheap.GetPageManager()
	pageID, _ := pm.Alloc()
	ptr := pm.PageIDToPtr(pageID)

	serializer := &PageSerializer{}
	_, err := serializer.Serialize(ptr, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPageLength)

	_, err = serializer.Serialize(ptr, MaxPagePayload+1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPageLength)
}

func TestPageSerializer_Deserialize_CRCError(t *testing.T) {
	require.NoError(t, offheap.InitPageManager(64*1024))
	pm := offheap.GetPageManager()
	pageID, _ := pm.Alloc()
	pa := offheap.NewPageAccessor(pm)
	pa.InitLeafPage(pageID, 1)

	ptr := pm.PageIDToPtr(pageID)
	serializer := &PageSerializer{}
	data, _ := serializer.Serialize(ptr, int(uint16(offheap.SizeofPageHeader)))

	data[len(data)-1] ^= 0xFF

	dstID, _ := pm.Alloc()
	dstPtr := pm.PageIDToPtr(dstID)
	_, err := serializer.Deserialize(data, dstPtr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCRCMismatch)
}

func TestPageSerializer_Deserialize_ShortData(t *testing.T) {
	serializer := &PageSerializer{}
	var dst [4096]byte
	_, err := serializer.Deserialize([]byte{0, 0, 0}, unsafe.Pointer(&dst[0]))
	require.Error(t, err)
}

func TestPageSerializer_Deserialize_NilDst(t *testing.T) {
	serializer := &PageSerializer{}
	data := make([]byte, MinDiskPageSize)
	_, err := serializer.Deserialize(data, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilDestination)
}
