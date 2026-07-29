package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AdvisorySeverity;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Size;

public record AttachAdvisoryRequestDto(
    @NotNull AdvisorySeverity severity,
    @NotBlank @Size(min = 1, max = 5000) String text) {}
