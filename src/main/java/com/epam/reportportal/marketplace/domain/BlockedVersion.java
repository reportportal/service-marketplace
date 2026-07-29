package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.time.Instant;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record BlockedVersion(String version, Instant blockedAt, String reason) {}
