package controllers

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "travel-swipe-backend/services"
)

func BookTrip(c *gin.Context) {
    destination := c.Query("destination")
    if destination == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "destination required"})
        return
    }
    booking := services.BookTrip(destination)
    c.JSON(http.StatusOK, booking)
}
