package main

import (
	"crypto/rand"
	"regexp"
)

// 32 chars (no O/I/0/1 — unambiguous when read aloud or typed).
// len 32 divides 256 evenly, so the modulo below has zero bias.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
const codeLen = 6

var codeRe = regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$`)

func newRoomCode() string {
	b := make([]byte, codeLen)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure means a broken host
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

func validCode(s string) bool { return codeRe.MatchString(s) }
