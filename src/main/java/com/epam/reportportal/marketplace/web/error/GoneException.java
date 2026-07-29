package com.epam.reportportal.marketplace.web.error;

import com.epam.reportportal.marketplace.web.dto.PluginTombstoneDto;

public class GoneException extends MarketplaceException {

  private final PluginTombstoneDto tombstone;

  public GoneException(PluginTombstoneDto tombstone) {
    super("GONE", "Plugin removed");
    this.tombstone = tombstone;
  }

  public PluginTombstoneDto getTombstone() {
    return tombstone;
  }
}
