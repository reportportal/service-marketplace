package com.epam.reportportal.marketplace.util;

import java.io.IOException;
import java.io.InputStream;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import org.apache.commons.codec.binary.Hex;

public final class Sha256Util {

  private Sha256Util() {}

  public static String hash(byte[] data) {
    try {
      MessageDigest digest = MessageDigest.getInstance("SHA-256");
      return Hex.encodeHexString(digest.digest(data)).toLowerCase();
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("SHA-256 not available", e);
    }
  }

  public static String hashStream(InputStream input) throws IOException {
    try {
      MessageDigest digest = MessageDigest.getInstance("SHA-256");
      try (DigestInputStream dis = new DigestInputStream(input, digest)) {
        byte[] buffer = new byte[8192];
        while (dis.read(buffer) != -1) {
          // consume stream; digest updated automatically
        }
      }
      return Hex.encodeHexString(digest.digest()).toLowerCase();
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("SHA-256 not available", e);
    }
  }
}
