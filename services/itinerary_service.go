package services

import (
    "travel-swipe-backend/models"
    "travel-swipe-backend/repositories"
)

func GenerateItinerary(destination string) models.Itinerary {
    return repositories.GenerateMockItinerary(destination)
}
