package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record IndexPluginEntry(
    String id,
    String name,
    String latestVersion,
    String description,
    PluginCategory category,
    AccessTier access,
    TrustTier tier) {}
