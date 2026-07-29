package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.AdvisoryJson;
import com.epam.reportportal.marketplace.domain.Author;
import com.epam.reportportal.marketplace.domain.Compatibility;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.domain.TrustTier;
import java.time.Instant;
import java.util.List;

public record PluginVersionDetailDto(
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
    boolean blocked,
    Instant blockedAt,
    String blockReason,
    SecurityAdvisoryDto advisory,
    String sha256,
    String changelogUrl,
    List<String> screenshotUrls) {

  public static PluginVersionDetailDto from(
      MarketplaceManifest manifest,
      TrustTier tier,
      boolean blocked,
      Instant blockedAt,
      String blockReason,
      AdvisoryJson advisoryJson,
      String sha256,
      String changelogUrl,
      List<String> screenshotUrls) {
    SecurityAdvisoryDto advisory = advisoryJson != null
        ? new SecurityAdvisoryDto(advisoryJson.severity(), advisoryJson.text(), advisoryJson.attachedAt())
        : null;
    return new PluginVersionDetailDto(
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
        blocked,
        blockedAt,
        blockReason,
        advisory,
        sha256,
        changelogUrl,
        screenshotUrls);
  }
}
