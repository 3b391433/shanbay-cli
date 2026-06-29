// Package decode ports shanbay web's response decoder ("bayDecode").
//
// Several apiv3.shanbay.com endpoints (today_learning_items, learning_items,
// unlearned_items, ...) return a body like {"data":"<enc>", ...} where <enc>
// is a custom-encoded string. Decode recovers the underlying JSON.
//
// Algorithm (ported from honwhy/shanbay-ext src/entrypoints/decodes.js, and
// verified end-to-end against live responses):
//
//  1. enc[0:4] is a "sign" used to seed a TinyMT-style PRNG; checkVersion gates it.
//  2. The PRNG deterministically assigns each of the 64 base64 chars a 1- or 2-char
//     base32 symbol, building a prefix tree (trie) of base32 -> base64.
//  3. Walking the trie over enc[4:] recovers a standard base64 string.
//  4. base64-decode that string -> UTF-8 JSON.
//
// All integer arithmetic is uint32: Go's natural uint32 wraparound reproduces the
// JS `>>> 0` masking and the 32-bit truncating multiply (Num.mul).
package decode

import (
	"encoding/base64"
	"errors"
)

// ErrNotEncoded is returned when enc fails the version check, which also happens
// for plain (unencoded) JSON bodies such as error responses.
var ErrNotEncoded = errors.New("decode: payload failed version check (not an encoded string)")

const (
	baySH0  = 1
	baySH1  = 10
	baySH8  = 8
	bayMask = 0x7FFFFFFF
	minLoop = 8
	preLoop = 8
	version = 1
	b32Code = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	b64Code = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

// cnt[(i+1)/11] is the base32 symbol length for the i-th base64 char:
// the first 10 chars get length 1, the rest length 2.
var cnt = [6]int{1, 2, 2, 2, 2, 2}

type random struct {
	status     [4]uint32
	mat1, mat2 uint32
	tmat       uint32
}

func (r *random) seed(sign string) {
	for i := range 4 {
		if i < len(sign) {
			r.status[i] = uint32(sign[i])
		} else {
			r.status[i] = 110
		}
	}
	r.mat1, r.mat2, r.tmat = r.status[1], r.status[2], r.status[3]
	r.init()
}

func (r *random) init() {
	for idx := range minLoop - 1 {
		prev := r.status[idx&3]
		i1 := (idx + 1) & 3
		r.status[i1] ^= uint32(idx+1) + 1812433253*(prev^(prev>>30))
	}
	if (r.status[0]&bayMask) == 0 && r.status[1] == 0 && r.status[2] == 0 && r.status[3] == 0 {
		r.status[0], r.status[1], r.status[2], r.status[3] = 66, 65, 89, 83
	}
	for range preLoop {
		r.nextState()
	}
}

func (r *random) nextState() {
	s0, s1, s2, s3 := r.status[0], r.status[1], r.status[2], r.status[3]
	y := s3
	x := (s0 & bayMask) ^ (s1 ^ s2)
	x ^= x << baySH0
	y ^= (y >> baySH0) ^ x
	// destructuring `[, status[0], status[1]] = status` shifts old s1,s2 down
	ns0 := s1
	ns1 := s2 ^ ((-(y & 1)) & r.mat1)
	ns2 := (x ^ (y << baySH1)) ^ ((-(y & 1)) & r.mat2)
	ns3 := y
	r.status[0], r.status[1], r.status[2], r.status[3] = ns0, ns1, ns2, ns3
}

func (r *random) generate(max uint32) uint32 {
	r.nextState()
	t0 := r.status[3]
	t1 := r.status[0] ^ (r.status[2] >> baySH8)
	t0 ^= t1
	t0 = ((-(t1 & 1)) & r.tmat) ^ t0
	return t0 % max
}

type node struct {
	char     byte
	children map[byte]*node
}

func newNode() *node { return &node{char: '.', children: map[byte]*node{}} }

type tree struct {
	rnd  random
	head *node
}

func (t *tree) initTree(sign string) {
	t.rnd.seed(sign)
	t.head = newNode()
	for i := range 64 {
		t.addSymbol(b64Code[i], cnt[(i+1)/11])
	}
}

func (t *tree) addSymbol(char byte, length int) {
	ptr := t.head
	for range length {
		inner := b32Code[t.rnd.generate(32)]
		// avoid colliding with an already-assigned leaf at this level
		for {
			child, ok := ptr.children[inner]
			if ok && child.char != '.' {
				inner = b32Code[t.rnd.generate(32)]
				continue
			}
			break
		}
		if _, ok := ptr.children[inner]; !ok {
			ptr.children[inner] = newNode()
		}
		ptr = ptr.children[inner]
	}
	ptr.char = char
}

func (t *tree) decodeTrie(enc string) string {
	out := make([]byte, 0, len(enc))
	for i := 4; i < len(enc); {
		if enc[i] == '=' {
			out = append(out, '=')
			i++
			continue
		}
		ptr := t.head
		for i < len(enc) {
			child, ok := ptr.children[enc[i]]
			if !ok {
				break
			}
			ptr = child
			i++
		}
		out = append(out, ptr.char)
	}
	return string(out)
}

func getIdx(c byte) int {
	if c >= 65 {
		return int(c) - 65
	}
	return int(c) - 65 + 41
}

func checkVersion(s string) bool {
	if len(s) < 4 {
		return false
	}
	wi := getIdx(s[0])*32 + getIdx(s[1])
	x := getIdx(s[2])
	check := getIdx(s[3])
	m := (wi*x + check) % 32
	if m < 0 {
		m += 32
	}
	return version >= m
}

// Decode recovers the JSON payload from an encoded string. It returns
// ErrNotEncoded if enc is not a valid encoded string (e.g. plain JSON).
func Decode(enc string) (string, error) {
	if !checkVersion(enc) {
		return "", ErrNotEncoded
	}
	var t tree
	t.initTree(enc[:4])
	raw := t.decodeTrie(enc)
	out, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
