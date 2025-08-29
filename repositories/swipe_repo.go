package repositories

import "travel-swipe-backend/models"

var destinations = []models.Destination{
    {ID: 1, Name: "Bali", Image: "bali.jpg"},
    {ID: 2, Name: "Paris", Image: "paris.jpg"},
    {ID: 3, Name: "Tokyo", Image: "tokyo.jpg"},
}

func GetDestinations() []models.Destination {
    return destinations
}
