package com.epam.reportportal.marketplace.web.dto;

import java.time.Instant;

public record BlockedArtifactErrorDto(boolean blocked, Instant blockedAt, String reason) {}
