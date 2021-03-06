/*
Copyright 2019 The Kubernetes Authors.
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package klogr implements github.com/go-logr/logr.Logger in terms of
// k8s.io/klog. It's a fork of klog/klogr because that serializes
// differently than klog itself.
//
// The formating of key/value pairs had to be copied from
// klog because there is no klog.InfosDepth and klog.ErrorsDepth
// and therefore klog cannot be called.
//
// This package can be removed once https://github.com/kubernetes/klog/pull/197
// is merged.
package klogr

import (
	"bytes"
	"fmt"
	"runtime"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
)

// New returns a logr.Logger which is implemented by klog.
func New() logr.Logger {
	return klogger{
		level:  0,
		prefix: "",
		values: nil,
	}
}

type klogger struct {
	level  int
	prefix string
	values []interface{}
}

func (l klogger) clone() klogger {
	return klogger{
		level:  l.level,
		prefix: l.prefix,
		values: copySlice(l.values),
	}
}

func copySlice(in []interface{}) []interface{} {
	out := make([]interface{}, len(in))
	copy(out, in)
	return out
}

// Magic string for intermediate frames that we should ignore.
const autogeneratedFrameName = "<autogenerated>"

// Discover how many frames we need to climb to find the caller. This approach
// was suggested by Ian Lance Taylor of the Go team, so it *should* be safe
// enough (famous last words).
func framesToCaller() int {
	// 1 is the immediate caller.  3 should be too many.
	for i := 1; i < 3; i++ {
		_, file, _, _ := runtime.Caller(i + 1) // +1 for this function's frame
		if file != autogeneratedFrameName {
			return i
		}
	}
	return 1 // something went wrong, this is safe
}

// trimDuplicates will deduplicates elements provided in multiple KV tuple
// slices, whilst maintaining the distinction between where the items are
// contained.
func trimDuplicates(kvLists ...[]interface{}) [][]interface{} {
	// maintain a map of all seen keys
	seenKeys := map[interface{}]struct{}{}
	// build the same number of output slices as inputs
	outs := make([][]interface{}, len(kvLists))
	// iterate over the input slices backwards, as 'later' kv specifications
	// of the same key will take precedence over earlier ones
	for i := len(kvLists) - 1; i >= 0; i-- {
		// initialise this output slice
		outs[i] = []interface{}{}
		// obtain a reference to the kvList we are processing
		kvList := kvLists[i]

		// start iterating at len(kvList) - 2 (i.e. the 2nd last item) for
		// slices that have an even number of elements.
		// We add (len(kvList) % 2) here to handle the case where there is an
		// odd number of elements in a kvList.
		// If there is an odd number, then the last element in the slice will
		// have the value 'null'.
		for i2 := len(kvList) - 2 + (len(kvList) % 2); i2 >= 0; i2 -= 2 {
			k := kvList[i2]
			// if we have already seen this key, do not include it again
			if _, ok := seenKeys[k]; ok {
				continue
			}
			// make a note that we've observed a new key
			seenKeys[k] = struct{}{}
			// attempt to obtain the value of the key
			var v interface{}
			// i2+1 should only ever be out of bounds if we handling the first
			// iteration over a slice with an odd number of elements
			if i2+1 < len(kvList) {
				v = kvList[i2+1]
			}
			// add this KV tuple to the *start* of the output list to maintain
			// the original order as we are iterating over the slice backwards
			outs[i] = append([]interface{}{k, v}, outs[i]...)
		}
	}
	return outs
}

// Serialization from klog.Infos (https://github.com/kubernetes/klog/blob/199a06da05a146312d7e58c2eeda84f069b1b932/klog.go#L798-L841).
const missingValue = "(MISSING)"

func kvListFormat(keysAndValues ...interface{}) string {
	b := bytes.Buffer{}
	for i := 0; i < len(keysAndValues); i += 2 {
		var v interface{}
		k := keysAndValues[i]
		if i+1 < len(keysAndValues) {
			v = keysAndValues[i+1]
		} else {
			v = missingValue
		}
		if i > 0 {
			b.WriteByte(' ')
		}

		switch v.(type) {
		case string, error:
			b.WriteString(fmt.Sprintf("%s=%q", k, v))
		default:
			if _, ok := v.(fmt.Stringer); ok {
				b.WriteString(fmt.Sprintf("%s=%q", k, v))
			} else {
				b.WriteString(fmt.Sprintf("%s=%+v", k, v))
			}
		}
	}
	return b.String()
}

func (l klogger) Info(msg string, kvList ...interface{}) {
	if l.Enabled() {
		trimmed := trimDuplicates(l.values, kvList)
		fixedStr := kvListFormat(trimmed[0]...)
		userStr := kvListFormat(trimmed[1]...)
		klog.InfoDepth(framesToCaller(), concatenate(l.prefix, msg, fixedStr, userStr)...)
	}
}

func (l klogger) Enabled() bool {
	return bool(klog.V(klog.Level(l.level)).Enabled())
}

func (l klogger) Error(err error, msg string, kvList ...interface{}) {
	errStr := kvListFormat("err", err)
	trimmed := trimDuplicates(l.values, kvList)
	fixedStr := kvListFormat(trimmed[0]...)
	userStr := kvListFormat(trimmed[1]...)
	klog.ErrorDepth(framesToCaller(), concatenate(l.prefix, msg, errStr, fixedStr, userStr)...)
}

func concatenate(prefix string, pieces ...string) []interface{} {
	var args []interface{}
	if prefix != "" {
		args = append(args, prefix+":")
	}
	for _, piece := range pieces {
		if piece != "" {
			if args != nil {
				args = append(args, " ")
			}
			args = append(args, piece)
		}
	}
	return args
}

func (l klogger) V(level int) logr.Logger {
	new := l.clone()
	new.level = level
	return new
}

// WithName returns a new logr.Logger with the specified name appended.  klogr
// uses '/' characters to separate name elements.  Callers should not pass '/'
// in the provided name string, but this library does not actually enforce that.
func (l klogger) WithName(name string) logr.Logger {
	new := l.clone()
	if len(l.prefix) > 0 {
		new.prefix = l.prefix + "/"
	}
	new.prefix += name
	return new
}

func (l klogger) WithValues(kvList ...interface{}) logr.Logger {
	new := l.clone()
	new.values = append(new.values, kvList...)
	return new
}

var _ logr.Logger = klogger{}
