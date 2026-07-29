package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.BlockedVersion;
import com.epam.reportportal.marketplace.domain.TrustTier;
import java.util.List;

public record PluginOperatorStateDto(
    String id, TrustTier tier, String latestVersion, List<BlockedVersion> blockedVersions) {}
