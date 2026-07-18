// notes.go — GNU ELF note section builders.
package elf

import "encoding/binary"

func BuildNoteSection(notes []Note) []byte {
	var buf []byte
	for _, n := range notes {
		namez := append([]byte(n.Name), 0)
		namesz := uint32(len(namez))
		descsz := uint32(len(n.Desc))
		var hdr [12]byte
		binary.LittleEndian.PutUint32(hdr[0:], namesz)
		binary.LittleEndian.PutUint32(hdr[4:], descsz)
		binary.LittleEndian.PutUint32(hdr[8:], n.Type)
		buf = append(buf, hdr[:]...)
		buf = notePad4(buf, namez)
		buf = notePad4(buf, n.Desc)
	}
	return buf
}

type Note struct {
	Name string
	Type uint32
	Desc []byte
}

func BuildBuildID(id []byte) []byte {
	return BuildNoteSection([]Note{{Name: "GNU", Type: NT_GNU_BUILD_ID, Desc: id}})
}

func BuildABITag(major, minor, patch uint32) []byte {
	desc := make([]byte, 16)
	binary.LittleEndian.PutUint32(desc[0:], GNU_ABI_TAG_LINUX)
	binary.LittleEndian.PutUint32(desc[4:], major)
	binary.LittleEndian.PutUint32(desc[8:], minor)
	binary.LittleEndian.PutUint32(desc[12:], patch)
	return BuildNoteSection([]Note{{Name: "GNU", Type: NT_GNU_ABI_TAG, Desc: desc}})
}

func BuildGNUProperty(featureFlags uint32) []byte {
	desc := make([]byte, 16)
	binary.LittleEndian.PutUint32(desc[0:], GNU_PROPERTY_X86_FEATURE_1_AND)
	binary.LittleEndian.PutUint32(desc[4:], 4)
	binary.LittleEndian.PutUint32(desc[8:], featureFlags)
	return BuildNoteSection([]Note{{Name: "GNU", Type: NT_GNU_PROPERTY, Desc: desc}})
}

func notePad4(buf, data []byte) []byte {
	buf = append(buf, data...)
	if r := len(buf) % 4; r != 0 {
		buf = append(buf, make([]byte, 4-r)...)
	}
	return buf
}