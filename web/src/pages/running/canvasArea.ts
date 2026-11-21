/**
 * The canvas's place on the page, shared by the canvas and the line that
 * stands in for it while its chunk loads (so nothing jumps when it arrives).
 * Down to the bottom of the window where the page above it leaves that much,
 * and never less than most of a screen: the canvas is fitted to this area,
 * and is as readable as the area is large.
 */
export const CANVAS_AREA = 'h-[max(70dvh,calc(100dvh-19rem))] min-h-80 w-full'
