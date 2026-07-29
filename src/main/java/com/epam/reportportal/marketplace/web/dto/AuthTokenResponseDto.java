package com.epam.reportportal.marketplace.web.dto;

public record AuthTokenResponseDto(String accessToken, String tokenType, long expiresIn) {}
