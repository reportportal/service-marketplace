package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.Author;
import com.epam.reportportal.marketplace.domain.Compatibility;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.domain.TrustTier;

public record PluginDetailDto(
    String id,
    String name,
    String version,
    String description,
    Author author,
    String license,
    PluginCategory category,
    Compatibility compatibility,
    String homepage,
    AccessTier access,
    String contactUrl,
    TrustTier tier,
    String latestVersion) {

  public static PluginDetailDto from(MarketplaceManifest manifest, TrustTier tier, String latestVersion) {
    return new PluginDetailDto(
        manifest.id(),
        manifest.name(),
        manifest.version(),
        manifest.description(),
        manifest.author(),
        manifest.license(),
        manifest.category(),
        manifest.compatibility(),
        manifest.homepage(),
        manifest.access(),
        manifest.contactUrl(),
        tier,
        latestVersion);
  }
}
