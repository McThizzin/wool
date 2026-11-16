package gguf

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

type valueKind int

const (
	kindUint valueKind = iota
	kindFloat
	kindString
	kindUintArray
)

type value struct {
	kind valueKind
	u    uint64
	f    float64
	s    string
	arr  []uint64
}

type reader struct {
	r *bufio.Reader
}

func (r *reader) u32() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b[:]), nil
}

func (r *reader) u64() (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func (r *reader) str() (string, error) {
	n, err := r.u64()
	if err != nil {
		return "", err
	}
	if n > 1<<24 {
		return "", fmt.Errorf("metadata string too large (%d bytes)", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *reader) skip(n uint64) error {
	if n > 1<<34 {
		return fmt.Errorf("metadata field too large (%d bytes)", n)
	}
	_, err := io.CopyN(io.Discard, r.r, int64(n))
	return err
}

// scalar reads a value that is not an array, returning its numeric or string
// representation. Types follow the GGUF metadata enum.
func (r *reader) scalar(t uint32) (value, error) {
	switch t {
	case 0: // uint8
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(b[0])}, nil
	case 1: // int8
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(int64(int8(b[0])))}, nil
	case 2: // uint16
		b := make([]byte, 2)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(binary.LittleEndian.Uint16(b))}, nil
	case 3: // int16
		b := make([]byte, 2)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(int64(int16(binary.LittleEndian.Uint16(b))))}, nil
	case 4: // uint32
		v, err := r.u32()
		return value{kind: kindUint, u: uint64(v)}, err
	case 5: // int32
		v, err := r.u32()
		return value{kind: kindUint, u: uint64(int64(int32(v)))}, err
	case 6: // float32
		v, err := r.u32()
		return value{kind: kindFloat, f: float64(math.Float32frombits(v))}, err
	case 7: // bool
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		var n uint64
		if b[0] != 0 {
			n = 1
		}
		return value{kind: kindUint, u: n}, nil
	case 8: // string
		s, err := r.str()
		return value{kind: kindString, s: s}, err
	case 10: // uint64
		v, err := r.u64()
		return value{kind: kindUint, u: v}, err
	case 11: // int64
		v, err := r.u64()
		return value{kind: kindUint, u: v}, err
	case 12: // float64
		v, err := r.u64()
		return value{kind: kindFloat, f: math.Float64frombits(v)}, err
	default:
		return value{}, fmt.Errorf("unsupported metadata type %d", t)
	}
}

func (r *reader) skipValue(t uint32) error {
	switch t {
	case 0, 1, 7:
		return r.skip(1)
	case 2, 3:
		return r.skip(2)
	case 4, 5, 6:
		return r.skip(4)
	case 8:
		_, err := r.str()
		return err
	case 10, 11, 12:
		return r.skip(8)
	case 9: // array
		et, err := r.u32()
		if err != nil {
			return err
		}
		count, err := r.u64()
		if err != nil {
			return err
		}
		if count > 1<<28 {
			return fmt.Errorf("metadata array too large (%d elements)", count)
		}
		switch et {
		case 0, 1, 7:
			return r.skip(count)
		case 2, 3:
			return r.skip(count * 2)
		case 4, 5, 6:
			return r.skip(count * 4)
		case 10, 11, 12:
			return r.skip(count * 8)
		case 8:
			for i := uint64(0); i < count; i++ {
				if _, err := r.str(); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unsupported array element type %d", et)
		}
	default:
		return fmt.Errorf("unsupported metadata type %d", t)
	}
}

func (r *reader) readValue(t uint32) (value, error) {
	if t != 9 {
		return r.scalar(t)
	}
	et, err := r.u32()
	if err != nil {
		return value{}, err
	}
	count, err := r.u64()
	if err != nil {
		return value{}, err
	}
	if count > 1<<28 {
		return value{}, fmt.Errorf("metadata array too large (%d elements)", count)
	}
	if et > 12 {
		return value{}, fmt.Errorf("unsupported array element type %d", et)
	}
	arr := make([]uint64, 0, min(int(count), 4096))
	for i := uint64(0); i < count; i++ {
		v, err := r.scalar(et)
		if err != nil {
			return value{}, err
		}
		if v.kind == kindUint {
			arr = append(arr, v.u)
		}
	}
	return value{kind: kindUintArray, arr: arr}, nil
}

// ReadMeta parses the metadata block of a GGUF file.
