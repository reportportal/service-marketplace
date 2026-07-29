package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
public class AssetsJson {

  private boolean hasChangelog;
  private List<String> screenshots = new ArrayList<>();
  private String sha256;
  private Instant publishedAt;

  public boolean isHasChangelog() {
    return hasChangelog;
  }

  public void setHasChangelog(boolean hasChangelog) {
    this.hasChangelog = hasChangelog;
  }

  public List<String> getScreenshots() {
    return screenshots;
  }

  public void setScreenshots(List<String> screenshots) {
    this.screenshots = screenshots;
  }

  public String getSha256() {
    return sha256;
  }

  public void setSha256(String sha256) {
    this.sha256 = sha256;
  }

  public Instant getPublishedAt() {
    return publishedAt;
  }

  public void setPublishedAt(Instant publishedAt) {
    this.publishedAt = publishedAt;
  }
}
