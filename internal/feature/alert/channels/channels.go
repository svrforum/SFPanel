package channels

import "errors"

// ErrChannelDelivery is returned when an outbound alert delivery fails for any
// transport reason. The wrapped cause is sanitized to never include the
// channel URL or credentials.
var ErrChannelDelivery = errors.New("alert channel delivery failed")
