package com.epam.reportportal.marketplace.web.dto;

import java.time.Instant;

public record PluginVersionSummaryDto(
    String version,
    Instant publishedAt,
    boolean blocked,
    Instant blockedAt,
    String blockReason) {}
