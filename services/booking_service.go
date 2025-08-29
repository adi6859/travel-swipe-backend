package services

import (
    "travel-swipe-backend/models"
    "travel-swipe-backend/repositories"
)

func BookTrip(destination string) models.Booking {
    return repositories.CreateBooking(destination)
}
