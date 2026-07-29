package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.util.CustomerIdentifiers;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Pattern;
import jakarta.validation.constraints.Size;
import java.time.LocalDate;

public record CreateLicenseRequestDto(
    @NotBlank
    @Size(min = 2, max = 64)
    @Pattern(regexp = CustomerIdentifiers.ID_REGEX, message = "must be a lowercase slug")
    String customerId,
    LocalDate expiresAt) {}
