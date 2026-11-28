// How far a handle's knob stays in from either side of the screen. A drag that
// starts at the edge is Android's back gesture: on a phone the selection often
// ends in the last column, the knob sat right there, and taking hold of it to
// move the end left the page instead.
export const KNOB_EDGE = 28
// A handle's touch target, square.
export const KNOB_TARGET = 40

// knobLeft is where a handle's 40px target goes for a selection end at `x`, on
// a screen `width` wide: centred under the end, kept KNOB_EDGE clear of the
// sides.
export function knobLeft(x: number, width: number): number {
  return Math.max(KNOB_EDGE, Math.min(x - KNOB_TARGET / 2, width - KNOB_TARGET - KNOB_EDGE))
}
