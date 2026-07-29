package com.epam.reportportal.marketplace.web.dto;

import java.time.LocalDate;

public record CreateLicenseRequestDto(String customerId, LocalDate expiresAt) {}
