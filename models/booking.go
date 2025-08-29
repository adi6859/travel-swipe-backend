package models

type Booking struct {
    BookingID   string `json:"booking_id"`
    Destination string `json:"destination"`
    Status      string `json:"status"`
}
