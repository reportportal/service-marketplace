package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AdvisorySeverity;
import java.time.Instant;

public record SecurityAdvisoryDto(AdvisorySeverity severity, String text, Instant attachedAt) {}
