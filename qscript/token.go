// qb: Neversoft QScript and PRE archive toolchain
// Author: epicmaxim <max@thug.one>

// Package qscript compiles and decompiles Neversoft QScript (.q source <-> .qb bytecode).
package qscript

// Token is a QScript bytecode token.
type Token int32

const (
	// Internal tokens, never written to a .qb
	tokRandomEnd    Token = -1
	tokRandomCase   Token = -2
	tokLabel        Token = -3
	tokNameChecksum Token = -4 // Name, except we don't know the name
	tokArgChecksum  Token = -5 // Arg, except we don't know the name

	TokEndOfFile        Token = 0
	TokEndOfLine        Token = 1
	TokEndOfLineNumber  Token = 2
	TokStartStruct      Token = 3
	TokEndStruct        Token = 4
	TokStartArray       Token = 5
	TokEndArray         Token = 6
	TokEquals           Token = 7
	TokDot              Token = 8
	TokComma            Token = 9
	TokMinus            Token = 10
	TokAdd              Token = 11
	TokDivide           Token = 12
	TokMultiply         Token = 13
	TokOpenParenth      Token = 14
	TokCloseParenth     Token = 15
	TokDebugInfo        Token = 16 // Ignored by the interpreter
	TokSameAs           Token = 17
	TokLessThan         Token = 18
	TokLessThanEqual    Token = 19
	TokGreaterThan      Token = 20
	TokGreaterThanEqual Token = 21

	// Types
	TokName        Token = 22
	TokInteger     Token = 23
	TokHexInteger  Token = 24 // Doesn't appear in any qb files
	TokEnum        Token = 25
	TokFloat       Token = 26
	TokString      Token = 27
	TokLocalString Token = 28
	TokArray       Token = 29
	TokVector      Token = 30
	TokPair        Token = 31

	// Keywords
	TokBegin     Token = 32
	TokRepeat    Token = 33
	TokBreak     Token = 34
	TokScript    Token = 35
	TokEndScript Token = 36
	TokIf        Token = 37
	TokElse      Token = 38
	TokElseIf    Token = 39 // Doesn't appear in any qb files
	TokEndIf     Token = 40
	TokReturn    Token = 41

	TokUndefined    Token = 42
	TokChecksumName Token = 43 // Checksum -> name table entry, for debugging
	TokAllArgs      Token = 44 // <...>
	TokArg          Token = 45 // Precedes a name enclosed in < > in the source
	TokJump         Token = 46 // Relative jump
	TokRandom       Token = 47 // Precedes a list of items to randomly choose from
	TokRandomRange  Token = 48 // Precedes two integers enclosed in parentheses
	TokAt           Token = 49 // Only used internally by qcomp, never appears in a .qb

	// Logical operators
	TokOr  Token = 50
	TokAnd Token = 51
	TokXor Token = 52

	// Shift operators
	TokShiftLeft  Token = 53
	TokShiftRight Token = 54

	// Rnd2 versions, so as not to mess up the determinism of Rnd in replays
	TokRandom2      Token = 55
	TokRandomRange2 Token = 56

	TokNot        Token = 57
	TokKeywordAnd Token = 58
	TokKeywordOr  Token = 59
	TokSwitch     Token = 60
	TokEndSwitch  Token = 61
	TokCase       Token = 62
	TokDefault    Token = 63

	TokRandomNoRepeat Token = 64
	TokRandomPermute  Token = 65

	TokColon Token = 66

	// THUG2 short jumps
	TokFastIf    Token = 71
	TokFastElse  Token = 72
	TokShortJump Token = 73

	// THAW: u16 size, then (4-byte aligned) a binary struct
	TokStruct Token = 74
)

// Target is the game a script is compiled for.
type Target int

const (
	THUG1 Target = iota
	THUG2
	THAW
)

// Targets maps -target names to targets.
var Targets = map[string]Target{
	"thug1": THUG1,
	"thug2": THUG2,
	"thaw":  THAW,
}

type targetProps struct {
	fastIfElseCase bool // IF/ELSE/CASE carry short jumps (FastIf, FastElse, ShortJump)
	thaw           bool // THAW: typed sections, binary structs, no checksum table
	inlineScript   bool // THAW script inside a struct: line ends carry line numbers
}

var targetPropsTable = map[Target]targetProps{
	THUG1: {},
	THUG2: {fastIfElseCase: true},
	THAW:  {fastIfElseCase: true, thaw: true},
}

// keywords maps every reserved word of the source language to its token.
// The decompiler quotes names that collide with these.
var keywords = map[string]Token{
	"BEGIN":            TokBegin,
	"REPEAT":           TokRepeat,
	"BREAK":            TokBreak,
	"SCRIPT":           TokScript,
	"ENDSCRIPT":        TokEndScript,
	"IF":               TokIf,
	"ELSE":             TokElse,
	"ELSEIF":           TokElseIf,
	"ENDIF":            TokEndIf,
	"RETURN":           TokReturn,
	"JUMP":             TokJump,
	"RANDOM_RANGE":     TokRandomRange,
	"RANDOM_RANGE2":    TokRandomRange2,
	"RANDOMEND":        tokRandomEnd,
	"RANDOMCASE":       tokRandomCase,
	"RANDOM_NO_REPEAT": TokRandomNoRepeat,
	"RANDOM_PERMUTE":   TokRandomPermute,
	"RANDOM2":          TokRandom2,
	"RANDOM":           TokRandom,
	"NOT":              TokNot,
	"AND":              TokKeywordAnd,
	"OR":               TokKeywordOr,
	"SWITCH":           TokSwitch,
	"ENDSWITCH":        TokEndSwitch,
	"CASE":             TokCase,
	"DEFAULT":          TokDefault,
	"PAIR":             TokPair,
	"VECTOR":           TokVector,
}
