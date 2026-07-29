package com.epam.reportportal.marketplace.web.dto;

import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Size;

public record RemovePluginRequestDto(
    @NotBlank @Size(min = 1, max = 2000) String removalReason) {}
