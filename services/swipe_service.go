package services

import (
    "travel-swipe-backend/models"
    "travel-swipe-backend/repositories"
)

func GetDestinations() []models.Destination {
    return repositories.GetDestinations()
}
