package controllers

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "travel-swipe-backend/services"
)

func GetDestinations(c *gin.Context) {
    destinations := services.GetDestinations()
    c.JSON(http.StatusOK, destinations)
}
