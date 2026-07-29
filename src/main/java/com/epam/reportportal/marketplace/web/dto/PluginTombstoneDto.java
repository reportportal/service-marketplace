package com.epam.reportportal.marketplace.web.dto;

import java.time.Instant;

public record PluginTombstoneDto(Instant removed, String removalReason, String removedBy) {}
