package com.epam.reportportal.marketplace.web.dto;

import java.util.List;

public record PublishBundle(
    byte[] jar,
    byte[] changelog,
    List<ScreenshotPart> screenshots) {

  public record ScreenshotPart(String filename, byte[] bytes) {}
}
