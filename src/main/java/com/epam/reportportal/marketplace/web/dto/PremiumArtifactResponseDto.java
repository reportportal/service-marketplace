package com.epam.reportportal.marketplace.web.dto;

import java.time.Instant;

public record PremiumArtifactResponseDto(String downloadUrl, Instant expiresAt) {}
