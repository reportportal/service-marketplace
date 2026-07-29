package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class PluginJson {

  private String id;
  private TrustTier tier = TrustTier.OFFICIAL;
  private String latestVersion;
  private List<String> versions = new ArrayList<>();
  private List<BlockedVersion> blockedVersions = new ArrayList<>();
  private Instant removed;
  private String removalReason;
  private String removedBy;

  public String getId() {
    return id;
  }

  public void setId(String id) {
    this.id = id;
  }

  public TrustTier getTier() {
    return tier;
  }

  public void setTier(TrustTier tier) {
    this.tier = tier;
  }

  public String getLatestVersion() {
    return latestVersion;
  }

  public void setLatestVersion(String latestVersion) {
    this.latestVersion = latestVersion;
  }

  public List<String> getVersions() {
    return versions;
  }

  public void setVersions(List<String> versions) {
    this.versions = versions;
  }

  public List<BlockedVersion> getBlockedVersions() {
    return blockedVersions;
  }

  public void setBlockedVersions(List<BlockedVersion> blockedVersions) {
    this.blockedVersions = blockedVersions;
  }

  public Instant getRemoved() {
    return removed;
  }

  public void setRemoved(Instant removed) {
    this.removed = removed;
  }

  public String getRemovalReason() {
    return removalReason;
  }

  public void setRemovalReason(String removalReason) {
    this.removalReason = removalReason;
  }

  public String getRemovedBy() {
    return removedBy;
  }

  public void setRemovedBy(String removedBy) {
    this.removedBy = removedBy;
  }

  public boolean isRemoved() {
    return removed != null;
  }
}
