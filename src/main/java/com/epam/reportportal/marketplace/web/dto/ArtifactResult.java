package com.epam.reportportal.marketplace.web.dto;

public record ArtifactResult(Type type, String redirectUrl, PremiumArtifactResponseDto premium) {

  public enum Type {
    REDIRECT,
    PREMIUM
  }

  public static ArtifactResult redirect(String url) {
    return new ArtifactResult(Type.REDIRECT, url, null);
  }

  public static ArtifactResult premium(PremiumArtifactResponseDto premium) {
    return new ArtifactResult(Type.PREMIUM, null, premium);
  }
}
