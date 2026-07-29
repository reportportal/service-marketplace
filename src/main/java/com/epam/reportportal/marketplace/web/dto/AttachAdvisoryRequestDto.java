package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AdvisorySeverity;

public record AttachAdvisoryRequestDto(AdvisorySeverity severity, String text) {}
