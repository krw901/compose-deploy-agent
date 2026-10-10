package main

import "flag"

// stringFlag registers a string flag under both a long name and a short
// alias on the same flag.FlagSet, both writing into the same variable. It
// exists purely as an ergonomics shortcut for developers running commands
// by hand (e.g. `-s`/`-t` instead of `--service`/`--tag`); the long form
// remains the documented, stable name.
func stringFlag(fs *flag.FlagSet, long, short, value, usage string) *string {
	p := fs.String(long, value, usage)
	fs.StringVar(p, short, value, "shorthand for --"+long)
	return p
}
