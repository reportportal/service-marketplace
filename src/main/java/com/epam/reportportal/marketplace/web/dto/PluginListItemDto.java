package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.domain.TrustTier;

public record PluginListItemDto(
    String id,
    String name,
    String latestVersion,
    String description,
    PluginCategory category,
    AccessTier access,
    TrustTier tier) {}
