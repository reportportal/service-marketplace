package com.epam.reportportal.marketplace.web.dto;

import java.util.List;

public record PluginVersionListResponseDto(String pluginId, List<PluginVersionSummaryDto> versions) {}
