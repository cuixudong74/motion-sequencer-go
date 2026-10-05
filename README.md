# Motion Sequencer

A deterministic, in-process animation timeline and blending engine for Go.

All sampling is performed in memory. Given the same timeline snapshot and the
same logical time, `Sample` always returns exactly the same floating-point
result, regardless of:

- previous sample times;
- sample frequency;
- call order of multiple samplers;
- whether a time was reached by increments or by an explicit seek.

## Core types

A `Timeline` contains named channels. Each channel contains weighted tracks.
Each track contains time-ordered clips, and each clip interpolates keyframes.

```go
timeline := motion.Timeline{
    Channels: []motion.Channel{{
        ID:        "channel-a",
        BlendMode: motion.BlendNormalized,
        Tracks: []motion.Track{{
            ID:     "track-a",
            Weight: 2,
            Easing: motion.EasingLinear,
            Clips: []motion.Clip{{
                ID:            "clip-a",
                Start:         0,
                Duration:      2,
                TransitionIn:  0.25,
                TransitionOut: 0.25,
                Keyframes: []motion.Keyframe{
                    {Time: 0, Value: 0},
                    {Time: 2, Value: 1},
                },
            }},
        }},
    }},
}
```

Supported easings are:

- `linear`;
- `ease_in` (`t*t*t`);
- `ease_out` (`1-(1-t)^3`);
- `ease_in_out`;
- empty string, treated as linear.

A clip easing is used for both keyframe interpolation and that clip's
transitions. If the clip easing is empty, the track easing is used.

## Sampling semantics

`Sample(at)` is pure with respect to the current immutable timeline snapshot.
`SampleNow()` samples the current playback position.

Keyframes must be strictly increasing by time. Values before the first
keyframe and after the last keyframe are held constant. Keyframe interpolation
uses the selected easing.

Times, durations, weights, values, rates and positions must be finite
floating-point numbers. Negative weights, non-finite values, duplicate IDs,
zero-length clips, or transitions longer than the clip are rejected before a
timeline snapshot becomes active.

## Clip layout and transitions

Clips may overlap on one track. At any boundary, the clip with the newest
`Start` has priority. Clip content intervals use the clip start inclusively
and the end as the limiting boundary, which lets an entering clip begin exactly
at the previous clip's ending value.

For each clip:

- `TransitionIn` fades from the older active track value at the entering
  clip's start to the new clip value;
- `TransitionOut` fades from the clip value to the next older active clip
  value, or zero if there is no older active value;
- a zero-duration transition is a hard switch;
- transition progress is clamped to `[0,1]` and eased;
- an exit transition must either overlap no older clip, in which case it
  deterministically fades to zero, or be covered by an older clip for its full
  duration, in which case it fades to that older value. Partial-only coverage
  is rejected to prevent an unselected jump during an exit transition.

The source for an entry transition is frozen at the entering clip's start.
Consequently, when an entry transition is interrupted by a newer entry, the
newer transition starts from one explicitly defined value. The engine never
stores intermediate transition state, so jumping to a time produces the same
result as stepping through every frame.

## Multi-track blending

Inactive or muted tracks do not participate. Active tracks with zero weight
also do not participate.

### `normalized`

This is the default. Active track values are combined as:

```text
sum(trackValue * trackWeight) / sum(trackWeight)
```

Changing one track's weight therefore changes its relative contribution.

### `additive`

Active track values are combined as:

```text
sum(trackValue * trackWeight)
```

If no track on a channel is active, the channel is zero.

## Playback

`Advance(deltaTime)` updates:

```text
position += deltaTime * rate
```

- Negative rates move backwards.
- `Seek(position)` jumps directly to an explicit logical position.
- `Pause()` prevents advances; `Play()` resumes them.
- Positive stepping to a time and seeking to that time produce identical
  samples because sampling is a pure function of logical time.
- Positions cannot become negative as the result of an advance.

`CommitPosition()` advances the durable recovery watermark to the current
position. The checkpoint is monotonic: explicitly rewinding the live position
does not move an already committed checkpoint backwards. The commit operation
is atomic with the rest of the engine state. An application should perform its
external work after obtaining the checkpoint and may replay that work after a
restart; the engine itself will never restore a position older than the
checkpoint.

## Runtime changes

Use the engine methods to replace a timeline or update tracks, clips, weights,
keyframes or transition parameters. Every change follows copy-on-write:

1. clone the active snapshot;
2. apply the change to the clone;
3. validate the complete resulting timeline;
4. atomically replace the active pointer.

Failed validation returns before publication, so readers continue to use the
old complete timeline. A sampler can never observe a half-updated slice.

## Concurrent readers

Samplers load one immutable snapshot pointer and call allocation-light, pure
sampling logic. No reader acquires a writer lock. A slow sampler therefore
does not block another sampler, delay a faster sampler, or prevent timeline
position advancement.

Mutators use an atomic compare-and-swap loop. If concurrent mutators race, the
loser retries against the newest committed snapshot instead of overwriting it.

## Save, load and compatibility

`Save` emits versioned JSON. The current version is `2`.

Version 2 contains:

- `version`;
- the complete timeline;
- playback position, committed position, rate and playing flag;
- the state revision.

On load:

1. the entire JSON document is decoded with unknown fields rejected;
2. version is recognized;
3. the complete timeline and playback state are validated;
4. only then is engine state atomically replaced.

Unknown newer versions return `ErrUnknownVersion` and do not modify current
state. Malformed JSON, unknown fields in recognized records, unsupported
options, invalid timelines or invalid playback state return
`ErrInvalidArchive` without publishing a partial timeline.

Older supported formats:

- version 1 timelines in the version envelope;
- version 1 timelines without playback information;
- versionless legacy timeline documents.

Older timelines receive current defaults: normalized blending, linear easing
and playback rate `1`.

After loading a valid archive, playback resumes at
`committed_position`. Positions after the last committed checkpoint are
discarded because they represent uncommitted work.

## Determinism notes

The implementation avoids global clocks, random seeds, goroutine-local state
and map-order-dependent evaluation. Floating-point operations are applied in a
fixed channel, track and clip order. Channels are returned in a map for caller
convenience, but the value of each channel is calculated independently and
deterministically.
