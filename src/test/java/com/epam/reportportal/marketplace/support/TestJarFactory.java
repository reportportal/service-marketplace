package com.epam.reportportal.marketplace.support;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.Author;
import com.epam.reportportal.marketplace.domain.Compatibility;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.service.ManifestExtractor;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.util.Random;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

public final class TestJarFactory {

  private TestJarFactory() {}

  public static byte[] createJar(MarketplaceManifest manifest) throws IOException {
    return createJar(manifest, 0);
  }

  /**
   * Builds a jar padded with {@code paddingBytes} of incompressible data, so the resulting
   * archive is guaranteed to be at least that large. Used to exercise upload size limits with
   * bundles closer to the size of a real plugin jar.
   */
  public static byte[] createJar(MarketplaceManifest manifest, int paddingBytes) throws IOException {
    ObjectMapper mapper = new ObjectMapper().registerModule(new JavaTimeModule());
    byte[] manifestBytes = mapper.writeValueAsBytes(manifest);
    ByteArrayOutputStream baos = new ByteArrayOutputStream();
    try (ZipOutputStream zos = new ZipOutputStream(baos)) {
      ZipEntry entry = new ZipEntry(ManifestExtractor.MANIFEST_ENTRY);
      zos.putNextEntry(entry);
      zos.write(manifestBytes);
      zos.closeEntry();
      if (paddingBytes > 0) {
        byte[] padding = new byte[paddingBytes];
        new Random(42).nextBytes(padding);
        zos.putNextEntry(new ZipEntry("BOOT-INF/classes/padding.bin"));
        zos.write(padding);
        zos.closeEntry();
      }
    }
    return baos.toByteArray();
  }

  public static MarketplaceManifest sampleManifest(String id, String version, AccessTier access) {
    return new MarketplaceManifest(
        id,
        "Sample Plugin",
        version,
        "Sample plugin description",
        new Author("ReportPortal", "support@reportportal.io", "https://reportportal.io"),
        "Apache-2.0",
        PluginCategory.BUG_TRACKING,
        new Compatibility(">=25.1"),
        "https://reportportal.io",
        access,
        access == AccessTier.PREMIUM ? "https://reportportal.io/pricing" : null);
  }
}
