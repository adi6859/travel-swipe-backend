package repositories

import (
    "fmt"
    "travel-swipe-backend/models"
)

func CreateBooking(destination string) models.Booking {
    return models.Booking{
        BookingID:   fmt.Sprintf("BOOK-%s-1234", destination),
        Destination: destination,
        Status:      "Confirmed",
    }
}
