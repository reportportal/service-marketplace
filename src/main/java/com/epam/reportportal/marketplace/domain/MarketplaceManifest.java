package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record MarketplaceManifest(
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
    String contactUrl) {

  public MarketplaceManifest {
    if (access == null) {
      access = AccessTier.PUBLIC;
    }
  }
}
