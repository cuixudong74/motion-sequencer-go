// Package motion implements a deterministic in-process motion timeline engine.
//
// Sampling is a pure function of an atomically published timeline and an
// absolute time. Multiple readers may use independent cursors; writers publish
// complete validated snapshots.
package motion
