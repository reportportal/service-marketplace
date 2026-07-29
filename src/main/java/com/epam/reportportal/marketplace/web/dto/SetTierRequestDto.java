package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.TrustTier;
import jakarta.validation.constraints.NotNull;

public record SetTierRequestDto(@NotNull TrustTier tier) {}
