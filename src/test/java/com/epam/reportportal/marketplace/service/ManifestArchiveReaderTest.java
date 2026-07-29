package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.epam.reportportal.marketplace.web.error.ValidationException;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;
import org.junit.jupiter.api.Test;

class ManifestArchiveReaderTest {

  private static final String MANIFEST = ManifestExtractor.MANIFEST_ENTRY;

  @Test
  void readsRequestedEntryFromAnArchiveWithinLimits() throws Exception {
    byte[] archive = zip(Map.of("other.txt", bytes("ignored"), MANIFEST, bytes("{\"id\":\"x\"}")));

    byte[] read = new ManifestArchiveReader().read(archive, MANIFEST);

    assertArrayEquals(bytes("{\"id\":\"x\"}"), read);
  }

  @Test
  void rejectsArchiveWithTooManyEntries() throws Exception {
    Map<String, byte[]> entries = new LinkedHashMap<>();
    for (int i = 0; i < 50; i++) {
      entries.put("filler-" + i + ".txt", new byte[] {1});
    }
    entries.put(MANIFEST, bytes("{}"));
    byte[] archive = zip(entries);

    ValidationException failure = assertThrows(
        ValidationException.class,
        () -> new ManifestArchiveReader(5, 1024, 1024 * 1024).read(archive, MANIFEST));

    assertTrue(
        failure.getErrors().stream().anyMatch(e -> e.message().contains("more than 5 entries")),
        failure.getErrors().toString());
  }

  @Test
  void rejectsOversizedManifestEntry() throws Exception {
    byte[] archive = zip(Map.of(MANIFEST, new byte[8192]));

    ValidationException failure = assertThrows(
        ValidationException.class,
        () -> new ManifestArchiveReader(100, 64, 1024 * 1024).read(archive, MANIFEST));

    assertTrue(
        failure.getErrors().stream().anyMatch(e -> e.message().contains("exceeds the maximum size")),
        failure.getErrors().toString());
  }

  @Test
  void rejectsArchiveThatInflatesPastTheBudgetWhileScanning() throws Exception {
    // Compresses to a few hundred bytes but inflates to 1 MiB, far past the 4 KiB budget —
    // the entry is skipped on the way to the manifest, so only a metered scan catches it.
    Map<String, byte[]> entries = new LinkedHashMap<>();
    entries.put("bomb.bin", new byte[1024 * 1024]);
    entries.put(MANIFEST, bytes("{}"));
    byte[] archive = zip(entries);
    assertTrue(archive.length < 4096, "fixture must stay small when compressed");

    ValidationException failure = assertThrows(
        ValidationException.class,
        () -> new ManifestArchiveReader(100, 1024, 4096).read(archive, MANIFEST));

    assertTrue(
        failure.getErrors().stream().anyMatch(e -> e.message().contains("decompressed bytes")),
        failure.getErrors().toString());
  }

  @Test
  void reportsMissingManifest() throws Exception {
    byte[] archive = zip(Map.of("other.txt", bytes("no manifest here")));

    ValidationException failure = assertThrows(
        ValidationException.class, () -> new ManifestArchiveReader().read(archive, MANIFEST));

    assertTrue(
        failure.getErrors().stream().anyMatch(e -> "manifest".equals(e.field())),
        failure.getErrors().toString());
  }

  private static byte[] zip(Map<String, byte[]> entries) throws IOException {
    ByteArrayOutputStream baos = new ByteArrayOutputStream();
    try (ZipOutputStream zos = new ZipOutputStream(baos)) {
      for (Map.Entry<String, byte[]> entry : entries.entrySet()) {
        zos.putNextEntry(new ZipEntry(entry.getKey()));
        zos.write(entry.getValue());
        zos.closeEntry();
      }
    }
    return baos.toByteArray();
  }

  private static byte[] bytes(String value) {
    return value.getBytes(StandardCharsets.UTF_8);
  }
}
